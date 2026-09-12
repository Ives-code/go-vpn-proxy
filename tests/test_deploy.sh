#!/usr/bin/env bash
set -Eeuo pipefail

root_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${root_dir}"

required_files=(
  .dockerignore
  Dockerfile
  config.container.example.yaml
  docker-compose.example.yml
  deploy/install-ubuntu.sh
  deploy/uninstall-ubuntu.sh
  deploy/dual-egress-gateway.service
  scripts/smoke-wsl.sh
  README.md
)
for file in "${required_files[@]}"; do
  [[ -f "${file}" ]] || {
    printf 'FAIL: missing %s\n' "${file}" >&2
    exit 1
  }
done

for ignored in '.env' 'config.yaml' 'config.container.yaml' '.git' 'state' 'bin'; do
  grep -Fxq -- "${ignored}" .dockerignore || {
    printf 'FAIL: .dockerignore must exclude %s\n' "${ignored}" >&2
    exit 1
  }
done

bash -n deploy/install-ubuntu.sh deploy/uninstall-ubuntu.sh scripts/smoke-wsl.sh

service_file="deploy/dual-egress-gateway.service"
for directive in \
  'User=dual-egress' \
  'NoNewPrivileges=true' \
  'PrivateTmp=true' \
  'ProtectSystem=strict' \
  'ProtectHome=true' \
  'ReadWritePaths=/var/lib/dual-egress-gateway' \
  'EnvironmentFile=/etc/dual-egress-gateway/gateway.env'; do
  grep -Fq -- "${directive}" "${service_file}" || {
    printf 'FAIL: missing systemd hardening: %s\n' "${directive}" >&2
    exit 1
  }
done

grep -Fq 'install -m 0600' deploy/install-ubuntu.sh || {
  printf 'FAIL: installer must create the environment file with mode 0600\n' >&2
  exit 1
}
grep -Fq 'LAN_CIDR' deploy/install-ubuntu.sh || {
  printf 'FAIL: installer must require an explicit LAN CIDR for UFW\n' >&2
  exit 1
}
validation_line="$(grep -n 'ipaddress.ip_network' deploy/install-ubuntu.sh | head -1 | cut -d: -f1)"
start_line="$(grep -n 'systemctl restart' deploy/install-ubuntu.sh | head -1 | cut -d: -f1)"
[[ -n "${validation_line}" && -n "${start_line}" && "${validation_line}" -lt "${start_line}" ]] || {
  printf 'FAIL: LAN_CIDR must be validated before the service starts\n' >&2
  exit 1
}
grep -Fq 'ufw --force delete allow from' deploy/uninstall-ubuntu.sh || {
  printf 'FAIL: uninstaller must remove the exact UFW rules created by install\n' >&2
  exit 1
}
grep -Fq 'readonly FIREWALL_STATE="${CONFIG_DIR}/ufw-cidr"' deploy/install-ubuntu.sh || {
  printf 'FAIL: firewall state must be stored in the root-managed config directory\n' >&2
  exit 1
}
if grep -Fq '${STATE_DIR}/ufw-cidr' deploy/install-ubuntu.sh; then
  printf 'FAIL: firewall state must not be stored in the service-writable state directory\n' >&2
  exit 1
fi
for marker in 'added_http_rule=1' 'added_ws_rule=1' 'restore_previous_firewall'; do
  grep -Fq -- "${marker}" deploy/install-ubuntu.sh || {
    printf 'FAIL: installer lacks transactional firewall marker %s\n' "${marker}" >&2
    exit 1
  }
done
for marker in 'backup_previous_install' 'restore_previous_install'; do
  grep -Fq -- "${marker}" deploy/install-ubuntu.sh || {
    printf 'FAIL: installer lacks upgrade rollback marker %s\n' "${marker}" >&2
    exit 1
  }
done
grep -Fq 'systemctl restart "${APP_NAME}.service"' deploy/install-ubuntu.sh || {
  printf 'FAIL: installer must restart an already-running service after upgrade\n' >&2
  exit 1
}
if grep -Eq 'ufw allow[[:space:]]+[0-9]+/tcp' deploy/install-ubuntu.sh; then
  printf 'FAIL: installer contains an unscoped UFW allow rule\n' >&2
  exit 1
fi
grep -Eq '^USER[[:space:]]+[0-9]+' Dockerfile || {
  printf 'FAIL: runtime container must use a numeric non-root user\n' >&2
  exit 1
}
if grep -Fq 'COPY . .' Dockerfile; then
  printf 'FAIL: Dockerfile must copy only build-required source directories\n' >&2
  exit 1
fi

if git grep -nE 'OGJj|e72cd6|subscribe\.php\?key=|get\.sushi2\.cloud/sushi/' \
  -- '*.go' '*.yaml' '*.yml' '*.sh' 'Dockerfile' ':!tests/test_deploy.sh'; then
  printf 'FAIL: a real subscription credential is present in tracked project content\n' >&2
  exit 1
fi

printf 'PASS: deployment safety checks succeeded\n'
