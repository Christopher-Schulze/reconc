#!/usr/bin/env bash

set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/reconc-release-publication-test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT HUP TERM

fail() {
  printf 'release-publication-test: %s\n' "$1" >&2
  exit 1
}

expect_failure() {
  if "$@" >/dev/null 2>&1; then
    fail "command unexpectedly succeeded: $*"
  fi
}

fake_bin="$tmp/bin"
mkdir -p "$fake_bin"
cat > "$fake_bin/gh" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail
printf '%q ' "$@" >> "$FAKE_GH_LOG"
printf '\n' >> "$FAKE_GH_LOG"
state_file="$FAKE_GH_STATE/release-state"
inventory_file="$FAKE_GH_STATE/inventory.tsv"
command="${1:-} ${2:-}"
case "$command" in
  "release view")
    [ -f "$state_file" ] || exit 1
    [ "$(cat "$state_file")" = draft ] && printf 'true\n' || printf 'false\n'
    ;;
  "release create")
    printf 'draft\n' > "$state_file"
    : > "$inventory_file"
    ;;
  "release edit")
    if printf '%s\n' "$@" | grep -Fqx -- '--draft=false'; then
      printf 'published\n' > "$state_file"
    else
      printf 'draft\n' > "$state_file"
    fi
    ;;
  "release delete-asset")
    name="$4"
    awk -F '\t' -v name="$name" '$1 != name' "$inventory_file" > "$inventory_file.next"
    mv "$inventory_file.next" "$inventory_file"
    ;;
  "release upload")
    [ "${FAKE_GH_UPLOAD_FAIL:-0}" != 1 ] || exit 41
    : > "$inventory_file"
    shift 3
    for path in "$@"; do
      name="${path##*/}"
      size="$(wc -c < "$path" | tr -d ' ')"
      digest="$(shasum -a 256 "$path" | awk '{print $1}')"
      printf '%s\t%s\tsha256:%s\n' "$name" "$size" "$digest" >> "$inventory_file"
    done
    LC_ALL=C sort -o "$inventory_file" "$inventory_file"
    ;;
  "api repos/test/repo/git/ref/tags/reconc-v1.2.3")
    ;;
  "api --paginate")
    if [ -f "$state_file" ]; then
      state="$(cat "$state_file")"
      draft=false
      [ "$state" = draft ] && draft=true
      printf 'reconc-v1.2.3\t%s\n' "$draft"
    fi
    ;;
  "api repos/test/repo/releases/tags/reconc-v1.2.3")
    if printf '%s\n' "$@" | grep -Fq '.assets[].name'; then
      cut -f1 "$inventory_file"
    else
      cat "$inventory_file"
      [ "${FAKE_GH_BAD_INVENTORY:-0}" != 1 ] || printf 'stale\t1\tsha256:%064d\n' 0
    fi
    ;;
  *)
    printf 'unexpected fake gh call: %s\n' "$*" >&2
    exit 42
    ;;
esac
SCRIPT
chmod +x "$fake_bin/gh"

dist="$tmp/dist"
notes="$tmp/notes.md"
mkdir -p "$dist"
printf 'asset\n' > "$dist/reconc-1.2.3-test"
printf '{}\n' > "$dist/release-manifest.json"
printf 'checksums\n' > "$dist/SHA256SUMS"
printf '# notes\n' > "$notes"

run_case() {
  case_name="$1"
  mode="$2"
  state="${3:-}"
  case_dir="$tmp/$case_name"
  mkdir -p "$case_dir"
  : > "$case_dir/log"
  : > "$case_dir/inventory.tsv"
  [ -z "$state" ] || printf '%s\n' "$state" > "$case_dir/release-state"
  PATH="$fake_bin:$PATH" \
    GITHUB_REPOSITORY=test/repo \
    FAKE_GH_STATE="$case_dir" \
    FAKE_GH_LOG="$case_dir/log" \
    "$root/scripts/release/publish-github-release.sh" reconc-v1.2.3 "$dist" "$notes" "$mode"
}

run_case new-release new
[ "$(cat "$tmp/new-release/release-state")" = published ] || fail "new release was not published"

stale_draft="$tmp/stale-draft"
mkdir -p "$stale_draft"
printf 'draft\n' > "$stale_draft/release-state"
printf 'stale\t5\tsha256:%064d\n' 0 > "$stale_draft/inventory.tsv"
: > "$stale_draft/log"
PATH="$fake_bin:$PATH" GITHUB_REPOSITORY=test/repo \
  FAKE_GH_STATE="$stale_draft" FAKE_GH_LOG="$stale_draft/log" \
  "$root/scripts/release/publish-github-release.sh" reconc-v1.2.3 "$dist" "$notes" new
grep -Fq 'release delete-asset' "$stale_draft/log" || fail "stale draft asset was not deleted"
grep -Fq $'stale\t' "$stale_draft/inventory.tsv" && fail "stale draft asset survived"

published="$tmp/unauthorized"
mkdir -p "$published"
printf 'published\n' > "$published/release-state"
: > "$published/inventory.tsv"
: > "$published/log"
expect_failure env PATH="$fake_bin:$PATH" GITHUB_REPOSITORY=test/repo \
  FAKE_GH_STATE="$published" FAKE_GH_LOG="$published/log" \
  "$root/scripts/release/publish-github-release.sh" reconc-v1.2.3 "$dist" "$notes" new
grep -Fq 'release upload' "$published/log" && fail "unauthorized replacement uploaded assets"
[ "$(cat "$published/release-state")" = published ] || fail "unauthorized replacement changed release state"

replacement="$tmp/replacement"
mkdir -p "$replacement"
printf 'published\n' > "$replacement/release-state"
printf 'stale\t5\tsha256:%064d\n' 0 > "$replacement/inventory.tsv"
: > "$replacement/log"
PATH="$fake_bin:$PATH" GITHUB_REPOSITORY=test/repo \
  FAKE_GH_STATE="$replacement" FAKE_GH_LOG="$replacement/log" \
  "$root/scripts/release/publish-github-release.sh" reconc-v1.2.3 "$dist" "$notes" replace
[ "$(cat "$replacement/release-state")" = published ] || fail "authorized replacement was not published"
grep -Fq 'release delete-asset' "$replacement/log" || fail "stale replacement asset was not deleted"
grep -Fq $'stale\t' "$replacement/inventory.tsv" && fail "stale replacement asset survived"
draft_line="$(grep -n 'release edit .*--draft ' "$replacement/log" | head -n 1 | cut -d: -f1)"
delete_line="$(grep -n 'release delete-asset' "$replacement/log" | head -n 1 | cut -d: -f1)"
publish_line="$(grep -n -- '--draft=false' "$replacement/log" | head -n 1 | cut -d: -f1)"
[ "$draft_line" -lt "$delete_line" ] && [ "$delete_line" -lt "$publish_line" ] \
  || fail "replacement did not remain draft through reconciliation"

bad="$tmp/bad-inventory"
mkdir -p "$bad"
: > "$bad/inventory.tsv"
: > "$bad/log"
expect_failure env PATH="$fake_bin:$PATH" GITHUB_REPOSITORY=test/repo \
  FAKE_GH_STATE="$bad" FAKE_GH_LOG="$bad/log" FAKE_GH_BAD_INVENTORY=1 \
  "$root/scripts/release/publish-github-release.sh" reconc-v1.2.3 "$dist" "$notes" new
[ "$(cat "$bad/release-state")" = draft ] || fail "inventory mismatch did not remain draft"
grep -Fq -- '--draft=false' "$bad/log" && fail "inventory mismatch was published"

upload_failure="$tmp/upload-failure"
mkdir -p "$upload_failure"
: > "$upload_failure/inventory.tsv"
: > "$upload_failure/log"
expect_failure env PATH="$fake_bin:$PATH" GITHUB_REPOSITORY=test/repo \
  FAKE_GH_STATE="$upload_failure" FAKE_GH_LOG="$upload_failure/log" FAKE_GH_UPLOAD_FAIL=1 \
  "$root/scripts/release/publish-github-release.sh" reconc-v1.2.3 "$dist" "$notes" new
[ "$(cat "$upload_failure/release-state")" = draft ] || fail "upload failure did not remain draft"
grep -Fq -- '--draft=false' "$upload_failure/log" && fail "upload failure was published"

missing_replace="$tmp/missing-replace"
mkdir -p "$missing_replace"
: > "$missing_replace/inventory.tsv"
: > "$missing_replace/log"
expect_failure env PATH="$fake_bin:$PATH" GITHUB_REPOSITORY=test/repo \
  FAKE_GH_STATE="$missing_replace" FAKE_GH_LOG="$missing_replace/log" \
  "$root/scripts/release/publish-github-release.sh" reconc-v1.2.3 "$dist" "$notes" replace

printf 'release-publication-test: ok\n'
