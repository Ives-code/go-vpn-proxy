#!/usr/bin/env bash
set -Eeuo pipefail

# Uses the existing private proxy credentials; never prints them.
environment_file="${GATEWAY_ENV_FILE:-/etc/dual-egress-gateway/gateway.env}"
if [[ -z "${PROXY_USERNAME:-}" || -z "${PROXY_PASSWORD:-}" ]]; then
  [[ -r "$environment_file" ]] || { printf 'Cannot read proxy credentials; run with sudo or export PROXY_USERNAME/PROXY_PASSWORD.\n' >&2; exit 1; }
  set -a
  # shellcheck disable=SC1090
  source "$environment_file"
  set +a
fi
export PROXY_USERNAME PROXY_PASSWORD
export FIXED_PROXY_URL="${FIXED_PROXY_URL:-http://127.0.0.1:18084}"

python3 - <<'PY'
import json
import os
import subprocess
import sys

target = 'https://www.okx.com/api/v5/public/time'
result = subprocess.run([
    'curl', '--silent', '--show-error', '--fail-with-body',
    '--connect-timeout', '10', '--max-time', '30', '--noproxy', '',
    '--proxy', os.environ['FIXED_PROXY_URL'],
    '--proxy-user', os.environ['PROXY_USERNAME'] + ':' + os.environ['PROXY_PASSWORD'],
    '--write-out', '\n%{http_code}\n%{time_total}', target,
], capture_output=True, text=True)
if result.returncode:
    print('FAIL: OKX request failed (curl exit %d).' % result.returncode, file=sys.stderr)
    print(result.stderr.strip(), file=sys.stderr)
    sys.exit(1)
try:
    body, status, elapsed = result.stdout.rsplit('\n', 2)
    payload = json.loads(body)
    timestamp = int(payload['data'][0]['ts'])
    assert status == '200' and payload['code'] == '0' and timestamp > 0
except (ValueError, KeyError, IndexError, TypeError, AssertionError):
    print('FAIL: OKX response did not contain a successful server timestamp.', file=sys.stderr)
    sys.exit(1)
print('PASS: fixed proxy -> OKX public time API')
print('HTTP: %s; OKX code: %s; elapsed: %ss; timestamp: %s' % (status, payload['code'], elapsed, timestamp))
PY
