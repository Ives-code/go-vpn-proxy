#!/usr/bin/env bash
set -Eeuo pipefail

root_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${root_dir}"

required_files=(
  Dockerfile
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
if grep -Eq 'ufw allow[[:space:]]+[0-9]+/tcp' deploy/install-ubuntu.sh; then
  printf 'FAIL: installer contains an unscoped UFW allow rule\n' >&2
  exit 1
fi
grep -Eq '^USER[[:space:]]+[0-9]+' Dockerfile || {
  printf 'FAIL: runtime container must use a numeric non-root user\n' >&2
  exit 1
}

if git grep -nE 'OGJj|e72cd6|subscribe\.php\?key=|get\.sushi2\.cloud/sushi/' \
  -- '*.go' '*.yaml' '*.yml' '*.sh' 'Dockerfile' ':!tests/test_deploy.sh'; then
  printf 'FAIL: a real subscription credential is present in tracked project content\n' >&2
  exit 1
fi

printf 'PASS: deployment safety checks succeeded\n'
