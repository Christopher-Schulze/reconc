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

expected_assets="_reconc reconc.1 reconc.bash reconc.fish completion-report.schema.json policy-config.schema.json policy-fix-plan.schema.json policy-lock-v1.schema.json policy-lock.schema.json policy-report.schema.json reconc-$version.spdx.json reconc-$version.cdx.json"
for target in "$@"; do
  os=${target%/*}
  arch=${target##*/}
  extension=""
  [ "$os" = "windows" ] && extension=".exe"
  expected_assets="$expected_assets $bin-$version-$os-$arch$extension"
done

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

commit=$(git -C "$root" rev-parse HEAD)
epoch=$(git -C "$root" show -s --format=%ct "$commit")
go_bin=${GO:-go}
"$go_bin" -C "$root" run ./scripts/release/sbom verify \
  --root "$root" \
  --output-dir "$dist" \
  --version "$version" \
  --commit "$commit" \
  --source-date-epoch "$epoch"
