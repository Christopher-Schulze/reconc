#!/usr/bin/env bash

set -euo pipefail

fail() {
  printf 'release-publication: %s\n' "$1" >&2
  exit 1
}

[ "$#" -eq 4 ] || fail "usage: $0 TAG DIST_DIR NOTES_FILE new|replace"

tag="$1"
dist="$2"
notes_file="$3"
mode="$4"
repository="${GITHUB_REPOSITORY:-Christopher-Schulze/reconc}"

[[ "$tag" =~ ^reconc-v[0-9]+\.[0-9]+\.[0-9]+$ ]] \
  || fail "tag must be stable reconc semantic versioning: $tag"
case "$mode" in
  new|replace) ;;
  *) fail "publication mode must be new or replace" ;;
esac
[[ "$repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] \
  || fail "GITHUB_REPOSITORY is invalid: $repository"
[ -d "$dist" ] && [ ! -L "$dist" ] || fail "release directory must be a real directory: $dist"
[ -f "$notes_file" ] && [ ! -L "$notes_file" ] || fail "release notes must be a real file: $notes_file"
[ -f "$dist/release-manifest.json" ] || fail "release-manifest.json is missing"
[ -f "$dist/SHA256SUMS" ] || fail "SHA256SUMS is missing"
command -v gh >/dev/null 2>&1 || fail "GitHub CLI is unavailable"

irregular="$(find "$dist" -mindepth 1 -maxdepth 1 ! -type f -print -quit)"
[ -z "$irregular" ] || fail "release inventory contains an irregular entry: $irregular"
shopt -s nullglob
assets=("$dist"/*)
[ "${#assets[@]}" -gt 0 ] || fail "release inventory is empty"

sha256_file() {
  local path="$1"
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$path" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$path" | awk '{print $1}'
  else
    fail "neither shasum nor sha256sum is available"
  fi
}

tmp="$(mktemp -d "${TMPDIR:-/tmp}/reconc-release-publication.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT INT HUP TERM
expected_inventory="$tmp/expected.tsv"
remote_inventory="$tmp/remote.tsv"
release_list="$tmp/releases.tsv"
remote_names="$tmp/remote-names.txt"

: > "$expected_inventory"
for asset in "${assets[@]}"; do
  name="${asset##*/}"
  case "$name" in
    ''|.*|*/*) fail "unsafe release asset name: $name" ;;
  esac
  size="$(wc -c < "$asset" | tr -d ' ')"
  digest="$(sha256_file "$asset")"
  printf '%s\t%s\tsha256:%s\n' "$name" "$size" "$digest" >> "$expected_inventory"
done
LC_ALL=C sort -o "$expected_inventory" "$expected_inventory"

gh api "repos/$repository/git/ref/tags/$tag" --silent >/dev/null \
  || fail "remote release tag could not be verified: $tag"
release_state="missing"
release_id=""
release_matches=0
load_release_list() {
  gh api --paginate "repos/$repository/releases?per_page=100" \
    --jq '.[] | [(.id | tostring), .tag_name, (.draft | tostring)] | @tsv' > "$release_list"
  [ "$(wc -l < "$release_list" | tr -d ' ')" -le 512 ] \
    || fail "remote release inventory exceeds the bounded lookup"
}

resolve_release() {
  release_id=""
  release_state="missing"
  release_matches=0
  while IFS=$'\t' read -r candidate_id release_tag release_draft extra; do
    [ -n "$release_tag" ] || continue
    [ -z "${extra:-}" ] || fail "release lookup returned malformed data"
    if [ "$release_tag" = "$tag" ]; then
      case "$candidate_id" in
        ''|*[!0-9]*) fail "release lookup returned an invalid release id: $candidate_id" ;;
      esac
      case "$release_draft" in
        true|false) ;;
        *) fail "release returned invalid draft state: $release_draft" ;;
      esac
      release_id="$candidate_id"
      release_state="$release_draft"
      release_matches=$((release_matches + 1))
    fi
  done < "$release_list"
  [ "$release_matches" -le 1 ] || fail "remote release lookup returned duplicate tags"
}

# The releases listing is eventually consistent: a release can be absent from
# it for a moment after it was created. Poll a bounded number of times so a
# normal API delay cannot be mistaken for a failed creation, and keep failing
# closed once the window is exhausted.
await_release_state() {
  attempt=0
  while [ "$attempt" -lt 10 ]; do
    load_release_list
    resolve_release
    [ "$release_state" = "missing" ] || return 0
    attempt=$((attempt + 1))
    sleep 3
  done
  return 1
}

load_release_list
resolve_release

case "$release_state:$mode" in
  missing:new)
    gh release create "$tag" \
      --title "reconc ${tag#reconc-v}" \
      --notes-file "$notes_file" \
      --verify-tag \
      --draft
    await_release_state || fail "new release did not appear in the release listing"
    [ "$release_state" = true ] || fail "new release was not created as a draft"
    ;;
  missing:replace)
    fail "replacement was requested but no release exists for $tag"
    ;;
  false:new)
    fail "release $tag is already published; rerun only with explicit replacement authorization"
    ;;
  false:replace)
    gh release edit "$tag" --draft
    [ "$(gh release view "$tag" --json isDraft --jq .isDraft)" = "true" ] \
      || fail "published release could not be moved back to draft"
    ;;
  true:new|true:replace)
    ;;
  *)
    fail "unsupported release transition: $release_state:$mode"
    ;;
esac
[ -n "$release_id" ] || fail "release id could not be resolved for $tag"
release_api="repos/$repository/releases/$release_id"

gh release edit "$tag" \
  --title "reconc ${tag#reconc-v}" \
  --notes-file "$notes_file" \
  --draft

gh api "$release_api" --jq '.assets[].name' > "$remote_names"
while IFS= read -r remote_name; do
  [ -n "$remote_name" ] || continue
  case "$remote_name" in
    *[![:print:]]*|*/*) fail "remote release contains an unsafe asset name" ;;
  esac
  gh release delete-asset "$tag" "$remote_name" --yes
done < "$remote_names"

gh release upload "$tag" "${assets[@]}"

verify_remote_inventory() {
  gh api "$release_api" \
    --jq '.assets[] | [.name, (.size | tostring), (.digest // "")] | @tsv' \
    | LC_ALL=C sort > "$remote_inventory"
  cmp -s "$expected_inventory" "$remote_inventory"
}

verified=false
for _ in 1 2 3; do
  if verify_remote_inventory; then
    verified=true
    break
  fi
  sleep 1
done
if [ "$verified" != true ]; then
  printf '%s\n' 'release-publication: expected remote inventory:' >&2
  cat "$expected_inventory" >&2
  printf '%s\n' 'release-publication: observed remote inventory:' >&2
  cat "$remote_inventory" >&2
  fail "uploaded release inventory does not match local artifacts"
fi

gh release edit "$tag" --draft=false
[ "$(gh release view "$tag" --json isDraft --jq .isDraft)" = "false" ] \
  || fail "release did not become published"
verify_remote_inventory || fail "published release inventory changed after verification"
printf 'release-publication: published %s with %d verified assets\n' "$tag" "${#assets[@]}"
