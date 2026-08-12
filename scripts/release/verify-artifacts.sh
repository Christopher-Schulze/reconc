#!/usr/bin/env sh

set -eu

[ "$#" -ge 4 ] || {
  printf 'usage: %s DIST_DIR BIN VERSION OS/ARCH...\n' "$0" >&2
  exit 2
}

dist="$1"
bin="$2"
version="$3"
shift 3
case "$dist" in
  /*) ;;
  *) dist="$(pwd)/$dist" ;;
esac
root=$(cd "$(dirname "$0")/../.." && pwd)
go_bin=${GO:-go}
manifest="$dist/SHA256SUMS"
[ -f "$manifest" ] || {
  printf 'error: checksum manifest not found: %s\n' "$manifest" >&2
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

# Repository-sourced assets come from the one manifest the build copies from,
# so the verifier cannot describe a different release than the build produces.
copied_manifest="$root/scripts/release/copied-assets.tsv"
[ -f "$copied_manifest" ] || {
  printf 'error: copied-asset manifest is missing: %s\n' "$copied_manifest" >&2
  exit 1
}
schema_assets=$("$go_bin" -C "$root" run ./scripts/release/schema-assets list-release)
copied_assets=""
copied_seen=""
record_copied_asset() {
  copied_name="$1"
  copied_source="$2"
  copied_extra="$3"
  [ -n "$copied_name" ] && [ -n "$copied_source" ] && [ -z "$copied_extra" ] || {
    printf 'error: malformed copied-asset entry: %s\n' "$copied_name" >&2
    exit 1
  }
  case "$copied_name" in
    */*|*'..'*)
      printf 'error: unsafe copied release asset name: %s\n' "$copied_name" >&2
      exit 1
      ;;
  esac
  case "$copied_source" in
    ''|/*|.|./*|*/.|*/./*|..|../*|*/..|*/../*|*\\*)
      printf 'error: unsafe copied release asset source: %s\n' "$copied_source" >&2
      exit 1
      ;;
  esac
  case " $copied_seen " in
    *" $copied_name "*)
      printf 'error: duplicate copied release asset mapping: %s\n' "$copied_name" >&2
      exit 1
      ;;
  esac
  copied_seen="$copied_seen $copied_name"
  copied_assets="$copied_assets $copied_name"
}

while IFS="$(printf '\t')" read -r copied_name copied_source copied_extra; do
  case "$copied_name" in
    ''|'#'*) continue ;;
  esac
  record_copied_asset "$copied_name" "$copied_source" "${copied_extra:-}"
done < "$copied_manifest"
while IFS="$(printf '\t')" read -r copied_name copied_source copied_extra; do
  record_copied_asset "$copied_name" "$copied_source" "${copied_extra:-}"
done <<EOF
$schema_assets
EOF
[ -n "$copied_assets" ] || {
  printf 'error: copied-asset manifest lists no artifacts\n' >&2
  exit 1
}
# Generated artifacts and binaries come from the same executable inventory the
# Makefile uses, so the verifier owns no second release-name list.
generated_assets=$(
  "$root/scripts/release/generated-assets.sh" list "$bin" "$version" "$@"
) || exit $?
expected_assets="$copied_assets"
while IFS= read -r name; do
  [ -n "$name" ] || continue
  expected_assets="$expected_assets $name"
done <<EOF
$generated_assets
EOF

seen=""
count=0
while read -r expected name extra; do
  [ -n "$expected" ] || continue
  [ -n "$name" ] && [ -z "${extra:-}" ] || {
    printf 'error: malformed checksum manifest entry\n' >&2
    exit 1
  }
  name=${name#\*}
  case "$name" in
    ''|*/*|*'..'*)
      printf 'error: unsafe artifact name in checksum manifest: %s\n' "$name" >&2
      exit 1
      ;;
  esac
  case " $seen " in
    *" $name "*)
      printf 'error: duplicate artifact in checksum manifest: %s\n' "$name" >&2
      exit 1
      ;;
  esac
  seen="$seen $name"
  [ "${#expected}" -eq 64 ] || {
    printf 'error: invalid SHA-256 digest for %s\n' "$name" >&2
    exit 1
  }
  case "$expected" in
    *[!0-9A-Fa-f]*)
      printf 'error: non-hexadecimal SHA-256 digest for %s\n' "$name" >&2
      exit 1
      ;;
  esac
  artifact="$dist/$name"
  [ -f "$artifact" ] || {
    printf 'error: checksummed artifact missing: %s\n' "$name" >&2
    exit 1
  }
  actual="$(sha256_file "$artifact")"
  [ "$actual" = "$expected" ] || {
    printf 'error: checksum mismatch for %s\n' "$name" >&2
    exit 1
  }
  count=$((count + 1))
done < "$manifest"

[ "$count" -gt 0 ] || {
  printf 'error: checksum manifest is empty\n' >&2
  exit 1
}

expected_count=0
for name in $expected_assets; do
  expected_count=$((expected_count + 1))
  case " $seen " in
    *" $name "*) ;;
    *)
      printf 'error: required release artifact is absent from manifest: %s\n' "$name" >&2
      exit 1
      ;;
  esac
done
[ "$count" -eq "$expected_count" ] || {
  printf 'error: manifest contains %s artifacts; expected exactly %s\n' "$count" "$expected_count" >&2
  exit 1
}

for artifact in "$dist"/*; do
  [ -f "$artifact" ] || continue
  name=${artifact##*/}
  [ "$name" = "SHA256SUMS" ] && continue
  case " $seen " in
    *" $name "*) ;;
    *)
      printf 'error: release artifact is not checksummed: %s\n' "$name" >&2
      exit 1
      ;;
  esac
done

"$go_bin" -C "$root" run ./scripts/release/manifest \
  --output-dir "$dist" \
  --version "$version" \
  --verify

commit=$(git -C "$root" rev-parse HEAD)
epoch=$(git -C "$root" show -s --format=%ct "$commit")
"$go_bin" -C "$root" run ./scripts/release/sbom verify \
  --root "$root" \
  --output-dir "$dist" \
  --version "$version" \
  --commit "$commit" \
  --source-date-epoch "$epoch"

surface_tmp=$(mktemp -d "${TMPDIR:-/tmp}/reconc-release-surface.XXXXXX")
trap 'rm -rf "$surface_tmp"' EXIT INT HUP TERM
GO="$go_bin" "$root/scripts/release/generated-assets.sh" generate completion \
  "$surface_tmp" "$version" "$commit" "$epoch"
GO="$go_bin" "$root/scripts/release/generated-assets.sh" generate manpage \
  "$surface_tmp" "$version" "$commit" "$epoch"
GO="$go_bin" "$root/scripts/release/generated-assets.sh" generate notices \
  "$surface_tmp" "$version" "$commit" "$epoch" "$@"
surface_names=$(
  "$root/scripts/release/generated-assets.sh" list-mode completion "$version"
  "$root/scripts/release/generated-assets.sh" list-mode manpage "$version"
  "$root/scripts/release/generated-assets.sh" list-mode notices "$version"
)
while IFS= read -r name; do
  [ -n "$name" ] || continue
  cmp -s "$surface_tmp/$name" "$dist/$name" || {
    printf 'error: release surface is stale or noncanonical: %s\n' "$name" >&2
    exit 1
  }
done <<EOF
$surface_names
EOF
verify_canonical_asset() {
  source="$1"
  name="$2"
  cmp -s "$source" "$dist/$name" || {
    printf 'error: release artifact is stale or noncanonical: %s\n' "$name" >&2
    exit 1
  }
}

# Every copied asset must be byte-identical to the repository file it claims to
# be a copy of, and the manifest states which file that is.
while IFS="$(printf '\t')" read -r copied_name copied_source copied_extra; do
  case "$copied_name" in
    ''|'#'*) continue ;;
  esac
  verify_canonical_asset "$root/$copied_source" "$copied_name"
done < "$copied_manifest"
while IFS="$(printf '\t')" read -r copied_name copied_source copied_extra; do
  verify_canonical_asset "$root/$copied_source" "$copied_name"
done <<EOF
$schema_assets
EOF
