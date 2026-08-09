#!/usr/bin/env sh
# reconc installer -- downloads the right binary for the current host
# and drops it in a stable user CLI directory.
#
# Usage:
#   sh install.sh                      # install the latest stable release
#   sh install.sh --channel preview    # install the latest preview
#   sh install.sh --version 0.9.5      # install one exact version
#   sh install.sh 0.9.5                # compatible exact-version form
#   RECONC_INSTALL_DIR=/tmp sh install.sh
#
# Pre-install-bootstrap exception: the installer must run before the Go binary
# exists. It stays minimal, POSIX-sh, and dependency-free so it works on macOS
# and Linux without bash-isms.

set -eu

REPOSITORY="Christopher-Schulze/reconc"
RELEASE_API_BASE="${RECONC_RELEASE_API_BASE:-https://api.github.com/repos/${REPOSITORY}}"
RELEASE_BASE="${RECONC_RELEASE_BASE:-https://github.com/Christopher-Schulze/reconc/releases/download}"
BIN_NAME="reconc"

log() { printf '>> %s\n' "$1" >&2; }
die() { printf 'error: %s\n' "$1" >&2; exit 1; }
usage() {
  printf '%s\n' \
    'usage: sh install.sh [--channel stable|preview | --version VERSION] [--allow-downgrade]' \
    '       sh install.sh VERSION'
}
shell_quote() {
  escaped="$(printf '%s' "$1" | sed "s/'/'\\\\''/g")" \
    || die "cannot quote the install directory for PATH remediation"
  printf "'%s'" "$escaped"
}
is_semantic_version() {
  version="$1"
  printf '%s\n' "$version" |
    grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$' \
    || return 1
  case "$version" in
    *-*)
      prerelease=${version#*-}
      old_ifs=$IFS
      IFS=.
      # shellcheck disable=SC2086 # The validated prerelease is intentionally split on dots.
      set -- $prerelease
      IFS=$old_ifs
      for identifier in "$@"; do
        case "$identifier" in
          0|*[!0-9]*) ;;
          0*) return 1 ;;
        esac
      done
      ;;
  esac
}
compare_versions() {
  LC_ALL=C awk -v left="$1" -v right="$2" '
    function numeric(value) { return value ~ /^[0-9]+$/ }
    function numericCompare(leftValue, rightValue) {
      sub(/^0+/, "", leftValue)
      sub(/^0+/, "", rightValue)
      if (leftValue == "") { leftValue = "0" }
      if (rightValue == "") { rightValue = "0" }
      if (length(leftValue) < length(rightValue)) { return -1 }
      if (length(leftValue) > length(rightValue)) { return 1 }
      if (("x" leftValue) < ("x" rightValue)) { return -1 }
      if (("x" leftValue) > ("x" rightValue)) { return 1 }
      return 0
    }
    BEGIN {
      split(left, leftParts, "-")
      split(right, rightParts, "-")
      split(leftParts[1], leftCore, ".")
      split(rightParts[1], rightCore, ".")
      for (i = 1; i <= 3; i++) {
        comparison = numericCompare(leftCore[i], rightCore[i])
        if (comparison != 0) { print comparison; exit }
      }
      leftPre = substr(left, length(leftParts[1]) + 2)
      rightPre = substr(right, length(rightParts[1]) + 2)
      if (leftPre == rightPre) { print 0; exit }
      if (leftPre == "") { print 1; exit }
      if (rightPre == "") { print -1; exit }
      leftCount = split(leftPre, leftIdentifiers, ".")
      rightCount = split(rightPre, rightIdentifiers, ".")
      count = leftCount < rightCount ? leftCount : rightCount
      for (i = 1; i <= count; i++) {
        if (leftIdentifiers[i] == rightIdentifiers[i]) { continue }
        if (numeric(leftIdentifiers[i]) && numeric(rightIdentifiers[i])) {
          print numericCompare(leftIdentifiers[i], rightIdentifiers[i])
          exit
        }
        if (numeric(leftIdentifiers[i])) { print -1; exit }
        if (numeric(rightIdentifiers[i])) { print 1; exit }
        print (leftIdentifiers[i] < rightIdentifiers[i] ? -1 : 1)
        exit
      }
      print (leftCount < rightCount ? -1 : 1)
    }
  '
}

channel=""
VERSION=""
allow_downgrade=false
while [ "$#" -gt 0 ]; do
  case "$1" in
    --channel)
      [ "$#" -ge 2 ] || die "--channel requires stable or preview"
      [ -z "$channel" ] || die "--channel may be specified only once"
      channel="$2"
      shift 2
      ;;
    --version)
      [ "$#" -ge 2 ] || die "--version requires a semantic version"
      [ -z "$VERSION" ] || die "--version may be specified only once"
      VERSION="$2"
      shift 2
      ;;
    --allow-downgrade)
      allow_downgrade=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    -*)
      die "unknown option: $1"
      ;;
    *)
      [ -z "$VERSION" ] || die "only one exact version may be selected"
      VERSION="$1"
      shift
      ;;
  esac
done
[ -z "$channel" ] || [ -z "$VERSION" ] \
  || die "--channel and --version are mutually exclusive"
if [ -n "$VERSION" ]; then
  is_semantic_version "$VERSION" || die "invalid semantic version: $VERSION"
  channel="exact"
else
  channel="${channel:-stable}"
  [ -z "${RECONC_RELEASE_BASE:-}" ] \
    || die "channel discovery is unavailable with RECONC_RELEASE_BASE; use --version"
  case "$channel" in
    stable)
      command -v curl >/dev/null 2>&1 \
        || die "stable channel discovery requires curl; use --version for an exact release"
      latest_url=$(
        curl -fsSL --proto '=https' --tlsv1.2 -o /dev/null -w '%{url_effective}' \
          "https://github.com/${REPOSITORY}/releases/latest"
      ) || die "stable release discovery failed"
      tag=${latest_url##*/}
      case "$latest_url" in
        "https://github.com/${REPOSITORY}/releases/tag/reconc-v"*) ;;
        *) die "stable release discovery returned a noncanonical URL: $latest_url" ;;
      esac
      VERSION=${tag#reconc-v}
      is_semantic_version "$VERSION" || die "stable release returned an invalid version: $VERSION"
      case "$VERSION" in *-*) die "stable release resolved to a prerelease: $VERSION" ;; esac
      ;;
    preview)
      command -v gh >/dev/null 2>&1 \
        || die "preview channel discovery requires GitHub CLI 'gh'; use --version for an exact preview"
      tag=$(
        gh api "${RELEASE_API_BASE#https://api.github.com/}/releases?per_page=32" \
          --jq 'map(select(.draft == false and .prerelease == true))[0].tag_name // empty'
      ) || die "preview release discovery failed"
      case "$tag" in reconc-v*) ;; *) die "no non-draft preview release is available" ;; esac
      VERSION=${tag#reconc-v}
      is_semantic_version "$VERSION" || die "preview release returned an invalid version: $VERSION"
      case "$VERSION" in *-*) ;; *) die "preview release is not a prerelease: $VERSION" ;; esac
      ;;
    *)
      die "channel must be stable or preview: $channel"
      ;;
  esac
fi

if [ -n "${RECONC_INSTALL_DIR:-}" ]; then
  INSTALL_DIR="$RECONC_INSTALL_DIR"
else
  [ -n "${HOME:-}" ] || die "HOME is unavailable; set RECONC_INSTALL_DIR explicitly"
  INSTALL_DIR="$HOME/.local/bin"
fi

# Detect OS.
case "$(uname -s)" in
  Darwin) os="darwin" ;;
  Linux)  os="linux"  ;;
  *)      die "unsupported OS: $(uname -s)" ;;
esac

# Detect arch.
case "$(uname -m)" in
  x86_64|amd64)  arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *)             die "unsupported arch: $(uname -m)" ;;
esac

asset="reconc-${VERSION}-${os}-${arch}"
release_tag="reconc-v${VERSION}"
url="${RELEASE_BASE}/${release_tag}/${asset}"
checksum_url="${RELEASE_BASE}/${release_tag}/SHA256SUMS"
log "target: ${os}/${arch}"
log "asset:  ${asset}"
log "url:    ${url}"

# Provenance: the release workflow publishes GitHub build-provenance
# attestations for every artifact listed in SHA256SUMS. When the GitHub
# CLI is available, the downloaded binary is verified against its
# attestation before installation, which breaks the
# binary-and-manifest-share-one-origin loop (the manifest is bound
# transitively through the checksum comparison). Optional by default;
# RECONC_REQUIRE_ATTESTATION=1 makes it mandatory.
ATTESTATION_TOOL="${RECONC_ATTESTATION_TOOL:-gh}"
ATTESTATION_REPO="${RECONC_ATTESTATION_REPO:-$REPOSITORY}"
attestation_state="embedded-verified"

verify_attestation() {
  artifact="$1"
  if ! command -v "$ATTESTATION_TOOL" >/dev/null 2>&1; then
    [ "${RECONC_REQUIRE_ATTESTATION:-0}" != "1" ] \
      || die "RECONC_REQUIRE_ATTESTATION=1 but '${ATTESTATION_TOOL}' is not installed"
    log "attestation: '${ATTESTATION_TOOL}' not found; skipping provenance verification (set RECONC_REQUIRE_ATTESTATION=1 to require it)"
    return 0
  fi
  if "$ATTESTATION_TOOL" attestation verify "$artifact" \
    --repo "$ATTESTATION_REPO" \
    --signer-workflow "$ATTESTATION_REPO/.github/workflows/reconc-release.yml" \
    --source-ref "refs/tags/$release_tag" \
    --deny-self-hosted-runners >/dev/null 2>&1; then
    attestation_state="github-verified"
    log "attestation: release binary provenance verified (${ATTESTATION_REPO})"
    return 0
  fi
  [ "${RECONC_REQUIRE_ATTESTATION:-0}" != "1" ] \
    || die "attestation verification failed for ${asset} (repo ${ATTESTATION_REPO})"
  log "attestation: WARNING verification failed or unavailable; continuing without provenance proof (set RECONC_REQUIRE_ATTESTATION=1 to make this fatal)"
}

download() {
  destination="$1"
  source_url="$2"
  maximum_bytes="$3"
  case "$maximum_bytes" in
    ''|*[!0-9]*) die "download limit is invalid: $maximum_bytes" ;;
  esac
  block_limit=$((maximum_bytes / 512 + 1))
  if command -v curl >/dev/null 2>&1; then
    (
      ulimit -f "$block_limit"
      curl -fL --proto '=https' --tlsv1.2 -o "$destination" "$source_url"
    ) \
      || die "download failed: curl returned $?"
  elif command -v wget >/dev/null 2>&1; then
    (
      ulimit -f "$block_limit"
      wget --https-only -O "$destination" "$source_url"
    ) \
      || die "download failed: wget returned $?"
  else
    die "neither curl nor wget available; install one and retry"
  fi
  downloaded_bytes=$(wc -c < "$destination" | tr -d ' ')
  [ "$downloaded_bytes" -le "$maximum_bytes" ] \
    || die "download exceeds ${maximum_bytes} bytes: ${source_url}"
}

sha256_file() {
  file="$1"
  if command -v shasum >/dev/null 2>&1; then
    output="$(shasum -a 256 "$file")" || return 1
  elif command -v sha256sum >/dev/null 2>&1; then
    output="$(sha256sum "$file")" || return 1
  else
    die "neither shasum nor sha256sum is available for checksum verification"
  fi
  hash=${output%% *}
  [ "$hash" != "$output" ] || return 1
  printf '%s\n' "$hash"
}

# Temp files, guaranteed cleanup.
tmp="$(mktemp -t reconc.XXXXXX)"
checksums="$(mktemp -t reconc-checksums.XXXXXX)"
trap 'rm -f "$tmp" "$checksums"' EXIT INT HUP TERM

download "$tmp" "$url" 268435456
download "$checksums" "$checksum_url" 2097152
verify_attestation "$tmp"

expected=""
matches=0
while read -r checksum filename extra; do
  filename=${filename#\*}
  if [ "$filename" = "$asset" ]; then
    expected="$checksum"
    matches=$((matches + 1))
    [ -z "${extra:-}" ] || die "malformed checksum entry for ${asset}"
  fi
done < "$checksums"

[ "$matches" -eq 1 ] || die "checksum manifest must contain exactly one entry for ${asset}; found ${matches}"
[ "${#expected}" -eq 64 ] || die "checksum for ${asset} is not a SHA-256 digest"
case "$expected" in
  *[!0-9A-Fa-f]*) die "checksum for ${asset} is not hexadecimal" ;;
esac

actual="$(sha256_file "$tmp")"
[ "$actual" = "$expected" ] || die "checksum mismatch for ${asset}"

chmod +x "$tmp"

# Verify it actually runs on this host before installing.
if ! "$tmp" --version >/dev/null 2>&1; then
  die "downloaded binary failed to execute; check the URL and host compatibility"
fi

# The verified candidate owns the binary transaction and global receipt. Needs
# write access to INSTALL_DIR; the caller handles privilege explicitly.
mkdir -p "$INSTALL_DIR" || die "install failed: cannot create ${INSTALL_DIR}"
target="${INSTALL_DIR}/${BIN_NAME}"
if [ -x "$target" ]; then
  installed_output="$("$target" --version 2>/dev/null || true)"
  case "$installed_output" in
    "reconc "*)
      installed_version=${installed_output#reconc }
      if is_semantic_version "$installed_version"; then
        comparison=$(compare_versions "$installed_version" "$VERSION")
        if [ "$comparison" -gt 0 ] && [ "$allow_downgrade" != true ]; then
          die "refusing downgrade from ${installed_version} to ${VERSION}; rerun with --allow-downgrade"
        fi
      fi
      ;;
  esac
fi
if install_output=$(
  RECONC_INSTALL_MANAGER=direct \
    RECONC_INSTALL_CHANNEL="$channel" \
    RECONC_INSTALL_ARTIFACT="$asset" \
    RECONC_INSTALL_RELEASE_TAG="$release_tag" \
    RECONC_INSTALL_PROVENANCE="$attestation_state" \
    "$tmp" install-cli --install-dir "$INSTALL_DIR" --json 2>&1
); then
  install_status=0
else
  install_status=$?
fi
[ -f "$target" ] && [ "$(sha256_file "$target")" = "$expected" ] \
  || die "install failed without publishing the verified binary: ${install_output}"

log "installed ${target}"
log "version: $("$target" --version)"
resolved="$(command -v reconc 2>/dev/null || true)"
resolved_hash=""
if [ -n "$resolved" ] && [ -f "$resolved" ] && [ "$resolved" -ef "$target" ]; then
  resolved_hash="$(sha256_file "$resolved" 2>/dev/null || true)"
fi
if [ "$resolved_hash" = "$expected" ]; then
  [ "$install_status" -eq 0 ] \
    || die "binary is current on PATH but ownership receipt publication failed: ${install_output}"
  log "next: reconc --help  or  reconc init ."
else
  log "PATH: add this line to your shell profile, then open a new terminal:"
  log "export PATH=$(shell_quote "$INSTALL_DIR"):\$PATH"
  log "until then: ${target} --help"
fi
