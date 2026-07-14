#!/usr/bin/env sh

set -eu

[ "$#" -eq 1 ] || {
  printf 'usage: %s DIST_DIR\n' "$0" >&2
  exit 2
}

dist="$1"
[ -d "$dist" ] || {
  printf 'error: release directory not found: %s\n' "$dist" >&2
  exit 1
}

manifest="$dist/SHA256SUMS"
tmp="$dist/.SHA256SUMS.tmp.$$"
paths="$dist/.SHA256SUMS.paths.$$"
trap 'rm -f "$tmp" "$paths"' EXIT INT HUP TERM

LC_ALL=C find "$dist" -type f \
  ! -name 'SHA256SUMS' ! -name '.SHA256SUMS.*' -print | LC_ALL=C sort > "$paths"
[ -s "$paths" ] || {
  printf 'error: no release artifacts found in %s\n' "$dist" >&2
  exit 1
}

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    output="$(shasum -a 256 "$1")" || return 1
  elif command -v sha256sum >/dev/null 2>&1; then
    output="$(sha256sum "$1")" || return 1
  else
    printf 'error: neither shasum nor sha256sum is available\n' >&2
    return 1
  fi
  hash=${output%% *}
  [ "$hash" != "$output" ] || return 1
  printf '%s\n' "$hash"
}

: > "$tmp"
while IFS= read -r path; do
  [ -f "$path" ] || {
    printf 'error: release artifact disappeared: %s\n' "$path" >&2
    exit 1
  }
  relative=${path#"$dist"/}
  case "$relative" in
    */*)
      printf 'error: release artifacts must be flat: %s\n' "$relative" >&2
      exit 1
      ;;
  esac
  name=${path##*/}
  hash="$(sha256_file "$path")"
  [ "${#hash}" -eq 64 ] || {
    printf 'error: invalid SHA-256 output for %s\n' "$name" >&2
    exit 1
  }
  printf '%s  %s\n' "$hash" "$name" >> "$tmp"
done < "$paths"

[ -s "$tmp" ] || {
  printf 'error: checksum manifest would be empty\n' >&2
  exit 1
}
mv "$tmp" "$manifest"
