#!/usr/bin/env bash
# Runs one shard of the repository's Go test packages so CI can spread one
# complete suite over parallel jobs.
#
# Usage: scripts/tests/go-test-shard.sh SHARD COUNT [go test flags...]
#
# Shards are numbered from 1. The slowest packages each lead a shard and the
# remaining root-module packages are dealt round-robin, so every package runs
# in exactly one shard. The last shard also runs the harness template module.

set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)

fail() {
  printf 'go-test-shard: %s\n' "$1" >&2
  exit 1
}

[ "$#" -ge 2 ] || fail "usage: go-test-shard.sh SHARD COUNT [go test flags...]"
shard=$1
count=$2
shift 2
case "$shard$count" in
  *[!0-9]*) fail "SHARD and COUNT must be positive integers" ;;
esac
[ "$count" -ge 1 ] && [ "$shard" -ge 1 ] && [ "$shard" -le "$count" ] \
  || fail "SHARD must be between 1 and COUNT"

# Ordered by race-test duration on the hosted Linux runner.
leading=(
  reconc.dev/reconc/internal/cli
  reconc.dev/reconc/internal/hooks
  reconc.dev/reconc/internal/usercli
)

cd "$root"
listed=$(go list ./...)
ordered=()
for package in "${leading[@]}"; do
  if printf '%s\n' "$listed" | grep -Fqx -- "$package"; then
    ordered+=("$package")
  fi
done
while IFS= read -r package; do
  case " ${leading[*]} " in
    *" $package "*) ;;
    *) ordered+=("$package") ;;
  esac
done <<<"$listed"

selected=()
for index in "${!ordered[@]}"; do
  if [ $((index % count + 1)) -eq "$shard" ]; then
    selected+=("${ordered[$index]}")
  fi
done

if [ "${#selected[@]}" -gt 0 ]; then
  go test "$@" "${selected[@]}"
fi
if [ "$shard" -eq "$count" ]; then
  (cd harness/template && go test "$@" ./...)
fi
