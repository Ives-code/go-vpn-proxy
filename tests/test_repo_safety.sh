#!/usr/bin/env bash
set -Eeuo pipefail

root_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${root_dir}"

main_file="cmd/dual-egress-gateway/main.go"
[[ -f "${main_file}" ]] || {
  printf 'FAIL: missing %s\n' "${main_file}" >&2
  exit 1
}

if git check-ignore -q -- "${main_file}"; then
  printf 'FAIL: source entrypoint is ignored by .gitignore\n' >&2
  git check-ignore -v -- "${main_file}" >&2
  exit 1
fi

for shell_file in deploy/install-ubuntu.sh deploy/uninstall-ubuntu.sh scripts/smoke-wsl.sh; do
  attribute="$(git check-attr eol -- "${shell_file}")"
  [[ "${attribute}" == *'eol: lf' ]] || {
    printf 'FAIL: %s is not forced to LF by .gitattributes\n' "${shell_file}" >&2
    exit 1
  }
done

printf 'PASS: repository safety checks succeeded\n'
