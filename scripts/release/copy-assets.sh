#!/usr/bin/env sh
set -eu

# Copies every repository-sourced release artifact into a distribution
# directory, using scripts/release/copied-assets.tsv as the only mapping. The
# build and the release trust fixture both call this, so neither can ship a set
# the artifact verifier does not expect.

if [ "$#" -ne 1 ]; then
  printf 'usage: %s DIST_DIR\n' "${0##*/}" >&2
  exit 64
fi

dist="$1"
root="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
manifest="$root/scripts/release/copied-assets.tsv"

[ -d "$dist" ] || {
  printf 'error: distribution directory does not exist: %s\n' "$dist" >&2
  exit 1
}
[ -f "$manifest" ] || {
  printf 'error: copied-asset manifest is missing: %s\n' "$manifest" >&2
  exit 1
}

copied=0
while IFS="$(printf '\t')" read -r name source extra; do
  case "$name" in
    ''|'#'*) continue ;;
  esac
  [ -n "$source" ] && [ -z "${extra:-}" ] || {
    printf 'error: malformed copied-asset entry: %s\n' "$name" >&2
    exit 1
  }
  case "$name" in
    */*|*'..'*)
      printf 'error: unsafe release asset name: %s\n' "$name" >&2
      exit 1
      ;;
  esac
  [ -f "$root/$source" ] || {
    printf 'error: release asset source is missing: %s\n' "$source" >&2
    exit 1
  }
  cp "$root/$source" "$dist/$name"
  copied=$((copied + 1))
done < "$manifest"

[ "$copied" -gt 0 ] || {
  printf 'error: copied-asset manifest lists no artifacts\n' >&2
  exit 1
}
