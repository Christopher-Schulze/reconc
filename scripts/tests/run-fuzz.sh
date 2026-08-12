#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
go_cmd="${GO:-go}"
fuzz_time="${FUZZ_TIME:-2s}"

if (($# != 0)); then
  printf 'usage: FUZZ_TIME=2s GO=go %s\n' "${0##*/}" >&2
  exit 64
fi
if [[ -z "$fuzz_time" ]]; then
  printf 'FUZZ_TIME must not be empty\n' >&2
  exit 64
fi

run_module() {
  local module_root="$1"
  local package target
  local count=0

  while IFS= read -r package; do
    while IFS= read -r target; do
      printf 'fuzz %s %s (%s)\n' "$package" "$target" "$fuzz_time"
      (
        cd "$module_root"
        "$go_cmd" test -run '^$' -fuzz "^${target}$" -fuzztime "$fuzz_time" "$package"
      )
      count=$((count + 1))
    done < <(
      cd "$module_root"
      "$go_cmd" test -run '^$' -list '^Fuzz' "$package" |
        awk '/^Fuzz[A-Za-z0-9_]+$/ { print }' |
        LC_ALL=C sort
    )
  done < <(cd "$module_root" && "$go_cmd" list ./... | LC_ALL=C sort)
  printf 'fuzz module %s: %d targets passed\n' "$module_root" "$count"
}

run_module "$root"
run_module "$root/harness/template"
