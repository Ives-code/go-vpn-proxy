#!/usr/bin/env bash
set -Eeuo pipefail

readonly APP_NAME="dual-egress-gateway"
readonly APP_USER="dual-egress"
readonly BINARY_DEST="/usr/local/bin/${APP_NAME}"
readonly CONFIG_DIR="/etc/${APP_NAME}"
readonly STATE_DIR="/var/lib/${APP_NAME}"
readonly SERVICE_DEST="/etc/systemd/system/${APP_NAME}.service"

die() {
  printf '[%s] ERROR: %s\n' "${APP_NAME}" "$*" >&2
  exit 1
}

[[ "${EUID}" -eq 0 ]] || die "run as root"
[[ -r /etc/os-release ]] || die "/etc/os-release is unavailable"
# shellcheck disable=SC1091
source /etc/os-release
[[ "${ID:-}" == "ubuntu" ]] || die "Ubuntu is required"
case "${VERSION_ID:-}" in
  20.04|22.04|24.04) ;;
  *) die "unsupported Ubuntu version: ${VERSION_ID:-unknown}" ;;
esac

binary_source="${1:-}"
config_source="${2:-}"
environment_source="${3:-}"
[[ -f "${binary_source}" ]] || die "binary path is required"
[[ -f "${config_source}" ]] || die "config path is required"
[[ -f "${environment_source}" ]] || die "environment file path is required"

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
service_source="${script_dir}/${APP_NAME}.service"
[[ -f "${service_source}" ]] || die "service template is missing"

if ! id "${APP_USER}" >/dev/null 2>&1; then
  useradd --system --home-dir "${STATE_DIR}" --shell /usr/sbin/nologin "${APP_USER}"
fi
install -d -o root -g "${APP_USER}" -m 0750 "${CONFIG_DIR}"
install -d -o "${APP_USER}" -g "${APP_USER}" -m 0750 "${STATE_DIR}"
install -o root -g root -m 0755 "${binary_source}" "${BINARY_DEST}"
install -o root -g "${APP_USER}" -m 0640 "${config_source}" "${CONFIG_DIR}/config.yaml"
install -m 0600 -o root -g root "${environment_source}" "${CONFIG_DIR}/gateway.env"
install -o root -g root -m 0644 "${service_source}" "${SERVICE_DEST}"

systemctl daemon-reload
systemctl enable --now "${APP_NAME}.service"

if [[ -n "${LAN_CIDR:-}" ]]; then
  command -v python3 >/dev/null 2>&1 || die "python3 is required to validate LAN_CIDR"
  python3 - "${LAN_CIDR}" <<'PY'
import ipaddress
import sys

network = ipaddress.ip_network(sys.argv[1], strict=False)
if network.prefixlen == 0:
    raise SystemExit("LAN_CIDR cannot allow the entire internet")
PY
  if command -v ufw >/dev/null 2>&1 && LC_ALL=C ufw status | grep -q '^Status: active$'; then
    ufw allow from "${LAN_CIDR}" to any port 18080 proto tcp
    ufw allow from "${LAN_CIDR}" to any port 18081 proto tcp
  fi
fi

printf '[%s] installed; inspect with: systemctl status %s\n' "${APP_NAME}" "${APP_NAME}"
