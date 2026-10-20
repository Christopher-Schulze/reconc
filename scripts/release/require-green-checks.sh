#!/usr/bin/env bash
# Refuses a release unless the required CI and CodeQL checks succeeded for the
# exact release commit, so the release workflow does not repeat the suites
# that CI already ran on that commit.
#
# Usage: scripts/release/require-green-checks.sh COMMIT
# Requires GITHUB_REPOSITORY and an authenticated gh CLI.

set -euo pipefail

fail() {
  printf 'require-green-checks: %s\n' "$1" >&2
  exit 1
}

[ "$#" -eq 1 ] || fail "usage: require-green-checks.sh COMMIT"
commit=$1
[[ "$commit" =~ ^[0-9a-f]{40}$ ]] || fail "commit must be a full lowercase object ID: $commit"
repository=${GITHUB_REPOSITORY:-}
[[ "$repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || fail "GITHUB_REPOSITORY must name owner/repository"

for check in "CI result" "Go security analysis"; do
  # Every latest run of the check on this commit must have succeeded, and at
  # least one must exist: a commit pushed as a branch and as a tag runs twice.
  conclusions=$(gh api -X GET "repos/$repository/commits/$commit/check-runs" \
    -f check_name="$check" -f filter=latest -F per_page=100 \
    --jq '.check_runs[] | select(.app.slug == "github-actions") | .conclusion // "pending"') \
    || fail "cannot read the $check check for $commit"
  [ -n "$conclusions" ] || fail "$check has not run for $commit"
  while IFS= read -r conclusion; do
    [ "$conclusion" = success ] || fail "$check for $commit concluded $conclusion"
  done <<<"$conclusions"
  printf 'require-green-checks: %s succeeded for %s\n' "$check" "$commit"
done
