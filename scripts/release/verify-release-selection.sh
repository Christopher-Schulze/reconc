#!/usr/bin/env bash
# Verifies that a release run starts from the selected stable tag, that the
# checkout is that tag's clean commit, and that the tag's release notes exist.
# The Release workflow runs it in a cheap preflight before the long gates and
# again in the job that builds and publishes the artifacts.
#
# Usage: RELEASE_TAG=reconc-vX.Y.Z scripts/release/verify-release-selection.sh
# Requires GITHUB_REF from the workflow run.

set -euo pipefail

fail() {
  printf 'verify-release-selection: %s\n' "$1" >&2
  exit 1
}

[ "$#" -eq 0 ] || fail "usage: RELEASE_TAG=reconc-vX.Y.Z verify-release-selection.sh"
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
tag=${RELEASE_TAG:-}
[[ "$tag" =~ ^reconc-v[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "input $tag is not a stable reconc release tag"
[ "${GITHUB_REF:-}" = "refs/tags/$tag" ] \
  || fail "the Release workflow must run from refs/tags/$tag, not ${GITHUB_REF:-an unset ref}"
version=$("$root/scripts/build/resolve-version.sh" "$root")
[ -f "$root/.github/releases/$tag.md" ] || fail "release notes are missing for $tag"
printf 'verify-release-selection: %s selects version %s with release notes\n' "$tag" "$version"
