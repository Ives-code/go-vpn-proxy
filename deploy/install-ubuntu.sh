#!/usr/bin/env bash
set -Eeuo pipefail

readonly APP_NAME="dual-egress-gateway"
readonly APP_USER="dual-egress"
readonly BINARY_DEST="/usr/local/bin/${APP_NAME}"
readonly CONFIG_DIR="/etc/${APP_NAME}"
readonly STATE_DIR="/var/lib/${APP_NAME}"
readonly SERVICE_DEST="/etc/systemd/system/${APP_NAME}.service"
readonly FIREWALL_STATE="${CONFIG_DIR}/ufw-cidr"

die() {
  printf '[%s] ERROR: %s\n' "${APP_NAME}" "$*" >&2
  exit 1
}

validate_cidr() {
  python3 - "$1" <<'PY'
import ipaddress
import sys

network = ipaddress.ip_network(sys.argv[1], strict=False)
if network.prefixlen == 0:
    raise SystemExit("LAN_CIDR cannot allow the entire internet")
PY
}

write_firewall_state() {
  local cidr="$1"
  local temp_state
  temp_state="$(mktemp "${CONFIG_DIR}/.ufw-cidr.XXXXXX")"
  printf '%s\n' "${cidr}" >"${temp_state}"
  chown root:root "${temp_state}"
  chmod 0600 "${temp_state}"
  mv -f -- "${temp_state}" "${FIREWALL_STATE}"
}

delete_firewall_rules() {
  local cidr="$1"
  ufw --force delete allow from "${cidr}" to any port 18080 proto tcp
  removed_old_http_rule=1
  ufw --force delete allow from "${cidr}" to any port 18081 proto tcp
  removed_old_ws_rule=1
}

add_firewall_rules() {
  local cidr="$1"
  ufw allow from "${cidr}" to any port 18080 proto tcp comment "${APP_NAME}"
  added_http_rule=1
  ufw allow from "${cidr}" to any port 18081 proto tcp comment "${APP_NAME}"
  added_ws_rule=1
}

backup_one() {
  local source="$1"
  local name="$2"
  if [[ -e "${source}" || -L "${source}" ]]; then
    cp -a -- "${source}" "${backup_dir}/${name}"
    printf -v "had_${name}" '%s' 1
  fi
}

restore_one() {
  local name="$1"
  local destination="$2"
  local present_var="had_${name}"
  if [[ "${!present_var}" -eq 1 ]]; then
    rm -f -- "${destination}"
    cp -a -- "${backup_dir}/${name}" "${destination}"
  else
    rm -f -- "${destination}"
  fi
}

backup_previous_install() {
  backup_dir="$(mktemp -d "/var/backups/${APP_NAME}.XXXXXX")"
  backup_one "${BINARY_DEST}" binary
  backup_one "${CONFIG_DIR}/config.yaml" config
  backup_one "${CONFIG_DIR}/gateway.env" environment
  backup_one "${SERVICE_DEST}" service
  if systemctl is-active --quiet "${APP_NAME}.service"; then
    was_active=1
  fi
}

restore_previous_firewall() {
  if [[ "${firewall_changed}" -ne 1 ]] || ! command -v ufw >/dev/null 2>&1; then
    return
  fi
  if [[ "${added_http_rule}" -eq 1 ]]; then
    ufw --force delete allow from "${validated_lan_cidr}" to any port 18080 proto tcp >/dev/null 2>&1 || true
  fi
  if [[ "${added_ws_rule}" -eq 1 ]]; then
    ufw --force delete allow from "${validated_lan_cidr}" to any port 18081 proto tcp >/dev/null 2>&1 || true
  fi
  if [[ "${removed_old_http_rule}" -eq 1 && -n "${old_cidr}" ]]; then
    ufw allow from "${old_cidr}" to any port 18080 proto tcp comment "${APP_NAME}" >/dev/null 2>&1 || true
  fi
  if [[ "${removed_old_ws_rule}" -eq 1 && -n "${old_cidr}" ]]; then
    ufw allow from "${old_cidr}" to any port 18081 proto tcp comment "${APP_NAME}" >/dev/null 2>&1 || true
  fi
  if [[ ( "${removed_old_http_rule}" -eq 1 || "${removed_old_ws_rule}" -eq 1 ) && -n "${old_cidr}" ]]; then
    write_firewall_state "${old_cidr}"
  elif [[ -z "${old_cidr}" ]]; then
    rm -f -- "${FIREWALL_STATE}"
  fi
}

restore_previous_install() {
  local exit_code="$?"
  [[ "${exit_code}" -ne 0 ]] || exit_code=1
  trap - ERR INT TERM HUP
  restore_previous_firewall
  restore_one binary "${BINARY_DEST}"
  restore_one config "${CONFIG_DIR}/config.yaml"
  restore_one environment "${CONFIG_DIR}/gateway.env"
  restore_one service "${SERVICE_DEST}"
  systemctl daemon-reload >/dev/null 2>&1 || true
  if [[ "${was_active}" -eq 1 ]]; then
    systemctl restart "${APP_NAME}.service" >/dev/null 2>&1 || true
  else
    systemctl stop "${APP_NAME}.service" >/dev/null 2>&1 || true
  fi
  if [[ "${was_enabled}" -eq 1 ]]; then
    systemctl enable "${APP_NAME}.service" >/dev/null 2>&1 || true
  else
    systemctl disable "${APP_NAME}.service" >/dev/null 2>&1 || true
  fi
  rm -rf -- "${backup_dir}"
  exit "${exit_code}"
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

validated_lan_cidr=""
if [[ -n "${LAN_CIDR:-}" ]]; then
  command -v python3 >/dev/null 2>&1 || die "python3 is required to validate LAN_CIDR"
  validate_cidr "${LAN_CIDR}"
  validated_lan_cidr="${LAN_CIDR}"
fi

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
service_source="${script_dir}/${APP_NAME}.service"
[[ -f "${service_source}" ]] || die "service template is missing"

# These variables are read through Bash indirect expansion in restore_one.
# shellcheck disable=SC2034
had_binary=0
# shellcheck disable=SC2034
had_config=0
# shellcheck disable=SC2034
had_environment=0
# shellcheck disable=SC2034
had_service=0
was_active=0
was_enabled=0
backup_dir=""
old_cidr=""
added_http_rule=0
added_ws_rule=0
removed_old_http_rule=0
removed_old_ws_rule=0
firewall_changed=0

backup_previous_install
if systemctl is-enabled --quiet "${APP_NAME}.service"; then
  was_enabled=1
fi
trap restore_previous_install ERR INT TERM HUP

if [[ -e "${FIREWALL_STATE}" || -L "${FIREWALL_STATE}" ]]; then
  [[ -f "${FIREWALL_STATE}" && ! -L "${FIREWALL_STATE}" ]] || die "firewall state must be a regular file"
  old_cidr="$(<"${FIREWALL_STATE}")"
  validate_cidr "${old_cidr}"
fi

if ! id "${APP_USER}" >/dev/null 2>&1; then
  useradd --system --home-dir "${STATE_DIR}" --shell /usr/sbin/nologin "${APP_USER}"
fi
install -d -o root -g "${APP_USER}" -m 0750 "${CONFIG_DIR}"
install -d -o "${APP_USER}" -g "${APP_USER}" -m 0750 "${STATE_DIR}"
install -o root -g root -m 0755 "${binary_source}" "${BINARY_DEST}"
install -o root -g "${APP_USER}" -m 0640 "${config_source}" "${CONFIG_DIR}/config.yaml"
install -m 0600 -o root -g root "${environment_source}" "${CONFIG_DIR}/gateway.env"
install -o root -g root -m 0644 "${service_source}" "${SERVICE_DEST}"

if [[ -n "${validated_lan_cidr}" ]] && command -v ufw >/dev/null 2>&1 && LC_ALL=C ufw status | grep -q '^Status: active$'; then
  if [[ "${validated_lan_cidr}" != "${old_cidr}" ]]; then
    firewall_changed=1
    if [[ -n "${old_cidr}" ]]; then
      delete_firewall_rules "${old_cidr}"
    fi
    add_firewall_rules "${validated_lan_cidr}"
    write_firewall_state "${validated_lan_cidr}"
  fi
fi

systemctl daemon-reload
systemctl enable "${APP_NAME}.service"
restart_count_before="$(systemctl show "${APP_NAME}.service" -p NRestarts --value 2>/dev/null || printf '0')"
systemctl restart "${APP_NAME}.service"
for _ in 1 2 3 4 5; do
  sleep 1
  systemctl is-active --quiet "${APP_NAME}.service" || die "service did not remain active after restart"
  main_pid="$(systemctl show "${APP_NAME}.service" -p MainPID --value)"
  [[ "${main_pid}" =~ ^[1-9][0-9]*$ ]] || die "service has no running main process"
done
restart_count_after="$(systemctl show "${APP_NAME}.service" -p NRestarts --value)"
[[ "${restart_count_after}" == "${restart_count_before}" ]] || die "service restarted unexpectedly during readiness window"
trap - ERR INT TERM HUP
rm -rf -- "${backup_dir}"

printf '[%s] installed; inspect with: systemctl status %s\n' "${APP_NAME}" "${APP_NAME}"
