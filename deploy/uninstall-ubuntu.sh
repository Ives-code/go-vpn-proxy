#!/usr/bin/env bash
set -Eeuo pipefail

readonly APP_NAME="dual-egress-gateway"
readonly CONFIG_DIR="/etc/${APP_NAME}"
readonly STATE_DIR="/var/lib/${APP_NAME}"
readonly FIREWALL_STATE="${CONFIG_DIR}/ufw-cidr"

[[ "${EUID}" -eq 0 ]] || { printf 'run as root\n' >&2; exit 1; }
systemctl disable --now "${APP_NAME}.service" 2>/dev/null || true

if [[ -e "${FIREWALL_STATE}" || -L "${FIREWALL_STATE}" ]]; then
  [[ -f "${FIREWALL_STATE}" && ! -L "${FIREWALL_STATE}" ]] || {
    printf 'refusing unsafe firewall state file\n' >&2
    exit 1
  }
  lan_cidr="$(<"${FIREWALL_STATE}")"
  python3 - "${lan_cidr}" <<'PY'
import ipaddress
import sys
ipaddress.ip_network(sys.argv[1], strict=False)
PY
  command -v ufw >/dev/null 2>&1 || {
    printf 'ufw is unavailable; preserving firewall ownership state at %s\n' "${FIREWALL_STATE}" >&2
    exit 1
  }
  ufw --force delete allow from "${lan_cidr}" to any port 18080 proto tcp || {
    printf 'failed to remove managed HTTP rule; preserving firewall ownership state\n' >&2
    exit 1
  }
  ufw --force delete allow from "${lan_cidr}" to any port 18081 proto tcp || {
    printf 'failed to remove managed WS rule; preserving firewall ownership state\n' >&2
    exit 1
  }
  rm -f -- "${FIREWALL_STATE}"
fi

rm -f -- "/etc/systemd/system/${APP_NAME}.service"
rm -f -- "/usr/local/bin/${APP_NAME}"
systemctl daemon-reload

if [[ "${1:-}" == "--purge" ]]; then
  [[ "${CONFIG_DIR}" == "/etc/dual-egress-gateway" ]] || exit 1
  [[ "${STATE_DIR}" == "/var/lib/dual-egress-gateway" ]] || exit 1
  rm -rf -- "${CONFIG_DIR}" "${STATE_DIR}"
  userdel "dual-egress" 2>/dev/null || true
  printf 'removed service, configuration, state, firewall rules, and service user\n'
else
  printf 'removed service, binary, and managed firewall rules; configuration and state were preserved\n'
fi
