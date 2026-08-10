#!/usr/bin/env sh
set -eu

# Copies every repository-sourced release artifact into a distribution
# directory. Non-schema mappings come from copied-assets.tsv; public schema
# mappings come from the typed Go registry. The build and release trust fixture
# both call this, so neither can ship a set the verifier does not expect.

if [ "$#" -ne 1 ]; then
  printf 'usage: %s DIST_DIR\n' "${0##*/}" >&2
  exit 64
fi

dist="$1"
root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
manifest="$root/scripts/release/copied-assets.tsv"
go_bin=${GO:-go}

[ -d "$dist" ] || {
  printf 'error: distribution directory does not exist: %s\n' "$dist" >&2
  exit 1
}
[ -f "$manifest" ] || {
  printf 'error: copied-asset manifest is missing: %s\n' "$manifest" >&2
  exit 1
}

copied=0
seen=""
copy_asset() {
  name="$1"
  source="$2"
  case "$name" in
    */*|*'..'*)
      printf 'error: unsafe release asset name: %s\n' "$name" >&2
      exit 1
      ;;
  esac
  case "$source" in
    ''|/*|.|./*|*/.|*/./*|..|../*|*/..|*/../*|*\\*)
      printf 'error: unsafe release asset source: %s\n' "$source" >&2
      exit 1
      ;;
  esac
  case " $seen " in
    *" $name "*)
      printf 'error: duplicate release asset mapping: %s\n' "$name" >&2
      exit 1
      ;;
  esac
  [ ! -L "$root/$source" ] || {
    printf 'error: release asset source is a symlink: %s\n' "$source" >&2
    exit 1
  }
  [ -f "$root/$source" ] || {
    printf 'error: release asset source is missing: %s\n' "$source" >&2
    exit 1
  }
  if [ -e "$dist/$name" ] || [ -L "$dist/$name" ]; then
    printf 'error: release asset destination already exists: %s\n' "$name" >&2
    exit 1
  fi
  cp "$root/$source" "$dist/$name"
  seen="$seen $name"
  copied=$((copied + 1))
}

while IFS="$(printf '\t')" read -r name source extra; do
  case "$name" in
    ''|'#'*) continue ;;
  esac
  [ -n "$source" ] && [ -z "${extra:-}" ] || {
    printf 'error: malformed copied-asset entry: %s\n' "$name" >&2
    exit 1
  }
  copy_asset "$name" "$source"
done < "$manifest"

schema_assets=$("$go_bin" -C "$root" run ./scripts/release/schema-assets list-release)
while IFS="$(printf '\t')" read -r name source extra; do
  [ -n "$name" ] && [ -n "$source" ] && [ -z "${extra:-}" ] || {
    printf 'error: malformed schema-asset entry: %s\n' "$name" >&2
    exit 1
  }
  copy_asset "$name" "$source"
done <<EOF
$schema_assets
EOF

[ "$copied" -gt 0 ] || {
  printf 'error: copied-asset manifest lists no artifacts\n' >&2
  exit 1
}
