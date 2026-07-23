#!/usr/bin/env sh
# reconc installer -- downloads the right binary for the current host
# and drops it in an install directory on PATH.
#
# Usage:
#   sh install.sh                      # install the current default version
#   sh install.sh 0.8.7                # pin version
#   RECONC_INSTALL_DIR=/tmp sh install.sh
#
# Pre-install-bootstrap exception: the installer must run before the Go binary
# exists. It stays minimal, POSIX-sh, and dependency-free so it works on macOS
# and Linux without bash-isms.

set -eu

VERSION="${1:-0.8.7}"
RELEASE_BASE="${RECONC_RELEASE_BASE:-https://github.com/Christopher-Schulze/reconc/releases/download}"
INSTALL_DIR="${RECONC_INSTALL_DIR:-/usr/local/bin}"
BIN_NAME="reconc"

log() { printf '>> %s\n' "$1" >&2; }
die() { printf 'error: %s\n' "$1" >&2; exit 1; }

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
url="${RELEASE_BASE}/reconc-v${VERSION}/${asset}"
checksum_url="${RELEASE_BASE}/reconc-v${VERSION}/SHA256SUMS"
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
ATTESTATION_REPO="${RECONC_ATTESTATION_REPO:-Christopher-Schulze/reconc}"

verify_attestation() {
  artifact="$1"
  if ! command -v "$ATTESTATION_TOOL" >/dev/null 2>&1; then
    [ "${RECONC_REQUIRE_ATTESTATION:-0}" != "1" ] \
      || die "RECONC_REQUIRE_ATTESTATION=1 but '${ATTESTATION_TOOL}' is not installed"
    log "attestation: '${ATTESTATION_TOOL}' not found; skipping provenance verification (set RECONC_REQUIRE_ATTESTATION=1 to require it)"
    return 0
  fi
  if "$ATTESTATION_TOOL" attestation verify "$artifact" --repo "$ATTESTATION_REPO" >/dev/null 2>&1; then
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
  if command -v curl >/dev/null 2>&1; then
    curl -fL --proto '=https' --tlsv1.2 -o "$destination" "$source_url" \
      || die "download failed: curl returned $?"
  elif command -v wget >/dev/null 2>&1; then
    wget --https-only -O "$destination" "$source_url" \
      || die "download failed: wget returned $?"
  else
    die "neither curl nor wget available; install one and retry"
  fi
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
staged=""
trap 'rm -f "$tmp" "$checksums"; if [ -n "$staged" ]; then rm -f "$staged"; fi' EXIT INT HUP TERM

download "$tmp" "$url"
download "$checksums" "$checksum_url"
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

# Install. Needs write access to INSTALL_DIR -- caller handles sudo if
# needed (we don't sudo implicitly, that's surprising behaviour).
mkdir -p "$INSTALL_DIR" || die "install failed: cannot create ${INSTALL_DIR}"
target="${INSTALL_DIR}/${BIN_NAME}"
staged="$(mktemp "${INSTALL_DIR}/.reconc.install.XXXXXX")" \
  || die "install failed: cannot stage a file in ${INSTALL_DIR}"
cp "$tmp" "$staged" || die "install failed: cannot copy the verified binary into ${INSTALL_DIR}"
chmod +x "$staged"
[ "$(sha256_file "$staged")" = "$expected" ] \
  || die "install failed: staged binary checksum changed"
if ! mv "$staged" "$target" 2>/dev/null; then
  die "install failed: cannot write to ${INSTALL_DIR}. Retry with 'sudo sh install.sh' or set RECONC_INSTALL_DIR=~/bin."
fi
staged=""

log "installed ${target}"
log "version: $("$target" --version)"
log "next: reconc --help  or  reconc bootstrap ."
