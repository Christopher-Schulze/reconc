#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
go_cmd="${GO:-go}"
root_profile="$root/coverage.out"
template_root="$root/harness/template"
template_profile="$template_root/coverage.out"
write_html=false

case "${1:-}" in
  "")
    ;;
  --html)
    write_html=true
    ;;
  *)
    printf 'usage: %s [--html]\n' "${0##*/}" >&2
    exit 64
    ;;
esac
if (( $# > 1 )); then
  printf 'usage: %s [--html]\n' "${0##*/}" >&2
  exit 64
fi

coverage_percent() {
  local profile="$1"
  awk '
    NR > 1 {
      block = $1
      statements[block] = $2
      counts[block] += $3
    }
    END {
      for (block in statements) {
        total += statements[block]
        if (counts[block] > 0) {
          covered += statements[block]
        }
      }
      if (total == 0) {
        exit 1
      }
      printf "%.4f\n", 100 * covered / total
    }
  ' "$profile"
}

(
  cd "$root"
  "$go_cmd" test -count=1 -covermode=atomic -coverpkg=./... -coverprofile="$root_profile" ./...
)
(
  cd "$template_root"
  "$go_cmd" test -count=1 -covermode=atomic -coverpkg=./... -coverprofile="$template_profile" ./...
)

root_actual="$(coverage_percent "$root_profile")"
template_actual="$(coverage_percent "$template_profile")"
printf 'root module coverage: %s%%\n' "$root_actual"
printf 'template module coverage: %s%%\n' "$template_actual"

if [[ "$write_html" == true ]]; then
  "$go_cmd" tool cover -html="$root_profile" -o "$root/coverage.html"
  (
    cd "$template_root"
    "$go_cmd" tool cover -html="$template_profile" -o coverage.html
  )
  printf 'HTML reports: %s, %s\n' "$root/coverage.html" "$template_root/coverage.html"
fi
