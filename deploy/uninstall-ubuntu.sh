#!/usr/bin/env bash
set -Eeuo pipefail

readonly APP_NAME="dual-egress-gateway"
readonly CONFIG_DIR="/etc/${APP_NAME}"
readonly STATE_DIR="/var/lib/${APP_NAME}"

[[ "${EUID}" -eq 0 ]] || { printf 'run as root\n' >&2; exit 1; }
systemctl disable --now "${APP_NAME}.service" 2>/dev/null || true
rm -f -- "/etc/systemd/system/${APP_NAME}.service"
rm -f -- "/usr/local/bin/${APP_NAME}"
systemctl daemon-reload

if [[ "${1:-}" == "--purge" ]]; then
  [[ "${CONFIG_DIR}" == "/etc/dual-egress-gateway" ]] || exit 1
  [[ "${STATE_DIR}" == "/var/lib/dual-egress-gateway" ]] || exit 1
  rm -rf -- "${CONFIG_DIR}" "${STATE_DIR}"
  userdel "dual-egress" 2>/dev/null || true
  printf 'removed service, configuration, state, and service user\n'
else
  printf 'removed service and binary; configuration and state were preserved\n'
fi
