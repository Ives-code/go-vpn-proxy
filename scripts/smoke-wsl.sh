#!/usr/bin/env bash
set -Eeuo pipefail

export GOTOOLCHAIN="${GOTOOLCHAIN:-go1.26.8}"

root_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${root_dir}"
environment_file="${GATEWAY_ENV_FILE:-.env}"
[[ -f "${environment_file}" ]] || { printf 'missing gateway environment file\n' >&2; exit 1; }

set -a
# shellcheck disable=SC1091
source "${environment_file}"
set +a

temp_dir="$(mktemp -d)"
gateway_pid=""
cleanup() {
  if [[ -n "${gateway_pid}" ]]; then
    kill "${gateway_pid}" 2>/dev/null || true
    wait "${gateway_pid}" 2>/dev/null || true
  fi
  rm -rf -- "${temp_dir}"
}
trap cleanup EXIT

cat >"${temp_dir}/config.yaml" <<'YAML'
http_listen: 127.0.0.1:28080
ws_listen: 127.0.0.1:28081
admin_listen: 127.0.0.1:29090
refresh_interval: 30m
probe_interval: 30s
dial_timeout: 10s
shutdown_drain: 5s
max_subscription_size: "4194304"
probe_url: https://cp.cloudflare.com/generate_204
YAML

go build -tags "with_quic with_utls with_grpc" -o "${temp_dir}/gateway" ./cmd/dual-egress-gateway
"${temp_dir}/gateway" -config "${temp_dir}/config.yaml" >"${temp_dir}/gateway.log" 2>&1 &
gateway_pid="$!"

ready=0
for _ in $(seq 1 120); do
  if curl -fsS http://127.0.0.1:29090/healthz >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 1
done
[[ "${ready}" -eq 1 ]] || {
  sed -E 's#https?://[^[:space:]]+#<redacted-url>#g' "${temp_dir}/gateway.log" >&2
  printf 'gateway did not become ready\n' >&2
  exit 1
}

for port in 28080 28081; do
  result_file="${temp_dir}/egress-${port}.txt"
  for _ in $(seq 1 4); do
    curl -fsS --max-time 30 \
      --proxy "http://127.0.0.1:${port}" \
      --proxy-user "${PROXY_USERNAME}:${PROXY_PASSWORD}" \
      -H 'Connection: close' \
      https://api.ipify.org >>"${result_file}"
    printf '\n' >>"${result_file}"
  done
  distinct_count="$(sort -u "${result_file}" | sed '/^$/d' | wc -l)"
  [[ "${distinct_count}" -ge 2 ]] || {
    printf 'listener %s did not demonstrate rotation across distinct egress addresses\n' "${port}" >&2
    exit 1
  }
done

status_json="$(curl -fsS http://127.0.0.1:29090/status)"
grep -Fq '"2":"source 2 returned HTTP 404"' <<<"${status_json}" || {
  printf 'source 2 HTTP 404 was not isolated in status output\n' >&2
  exit 1
}

printf 'PASS: WSL live subscription smoke test succeeded on both proxy listeners\n'
