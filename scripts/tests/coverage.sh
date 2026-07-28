#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
go_cmd="${GO:-go}"
root_min="${RECONC_ROOT_COVERAGE_MIN:-83.9}"
template_min="${RECONC_TEMPLATE_COVERAGE_MIN:-85.0}"
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

validate_threshold() {
  local name="$1"
  local value="$2"
  if [[ ! "$value" =~ ^([0-9]{1,2}([.][0-9]+)?|100([.]0+)?)$ ]]; then
    printf '%s must be a percentage from 0 through 100, got %q\n' "$name" "$value" >&2
    exit 64
  fi
}

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

enforce_floor() {
  local name="$1"
  local actual="$2"
  local minimum="$3"
  if ! awk -v actual="$actual" -v minimum="$minimum" 'BEGIN { exit !(actual >= minimum) }'; then
    printf '%s coverage %s%% is below the required %s%% floor\n' "$name" "$actual" "$minimum" >&2
    exit 1
  fi
  printf '%s coverage: %s%% (required: %s%%)\n' "$name" "$actual" "$minimum"
}

validate_threshold RECONC_ROOT_COVERAGE_MIN "$root_min"
validate_threshold RECONC_TEMPLATE_COVERAGE_MIN "$template_min"

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
enforce_floor "root module" "$root_actual" "$root_min"
enforce_floor "template module" "$template_actual" "$template_min"

if [[ "$write_html" == true ]]; then
  "$go_cmd" tool cover -html="$root_profile" -o "$root/coverage.html"
  (
    cd "$template_root"
    "$go_cmd" tool cover -html="$template_profile" -o coverage.html
  )
  printf 'HTML reports: %s, %s\n' "$root/coverage.html" "$template_root/coverage.html"
fi
