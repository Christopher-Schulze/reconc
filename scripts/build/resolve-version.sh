#!/usr/bin/env bash
set -euo pipefail

# Only an explicitly selected existing tag assigns a product release version.
root="${1:-$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)}"
fail() {
  printf 'resolve-version: %s\n' "$1" >&2
  exit 64
}
(( $# <= 1 )) || fail 'usage: resolve-version.sh [ROOT]'
commit="$(git -C "$root" rev-parse --verify HEAD)"
state="$(git -C "$root" status --porcelain --untracked-files=normal)"
if [ -z "${RELEASE_TAG:-}" ]; then
  identity="dev+${commit:0:12}"
  [ -z "$state" ] || identity="$identity-dirty"
  printf '%s\n' "$identity"
  exit 0
fi
[[ "$RELEASE_TAG" =~ ^reconc-v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || fail 'RELEASE_TAG must be an exact stable reconc-vX.Y.Z tag'
tag_commit="$(git -C "$root" rev-parse --verify "refs/tags/$RELEASE_TAG^{commit}")" || fail 'selected release tag does not exist'
[ "$tag_commit" = "$commit" ] || fail 'selected release tag does not identify HEAD'
[ -z "$state" ] || fail 'release source has tracked or untracked changes'
printf '%s\n' "${RELEASE_TAG#reconc-v}"
