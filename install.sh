#!/usr/bin/env sh
# reconc installer -- downloads the right binary for the current host
# and drops it in an install directory on PATH.
#
# Usage:
#   sh install.sh                      # install the current default version
#   sh install.sh 0.4.0                # pin version
#   RECONC_INSTALL_DIR=/tmp sh install.sh
#
# Pre-install-bootstrap exception: this is the one shell script in
# reconc/ (everything else is Go). Kept minimal, POSIX-sh, and
# dependency-free so it works on macOS / Linux without bash-isms.

set -eu

VERSION="${1:-0.4.0}"
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
log "target: ${os}/${arch}"
log "asset:  ${asset}"
log "url:    ${url}"

# Temp file, guaranteed cleanup.
tmp="$(mktemp -t reconc.XXXXXX)"
trap 'rm -f "$tmp"' EXIT INT HUP TERM

# Prefer curl, fall back to wget.
if command -v curl >/dev/null 2>&1; then
  curl -fL --proto '=https' --tlsv1.2 -o "$tmp" "$url" \
    || die "download failed: curl returned $?"
elif command -v wget >/dev/null 2>&1; then
  wget --https-only -O "$tmp" "$url" \
    || die "download failed: wget returned $?"
else
  die "neither curl nor wget available; install one and retry"
fi

chmod +x "$tmp"

# Verify it actually runs on this host before installing.
if ! "$tmp" --version >/dev/null 2>&1; then
  die "downloaded binary failed to execute; check the URL and host compatibility"
fi

# Install. Needs write access to INSTALL_DIR -- caller handles sudo if
# needed (we don't sudo implicitly, that's surprising behaviour).
mkdir -p "$INSTALL_DIR" 2>/dev/null || true
target="${INSTALL_DIR}/${BIN_NAME}"
if ! mv "$tmp" "$target" 2>/dev/null; then
  die "install failed: cannot write to ${INSTALL_DIR}. Retry with 'sudo sh install.sh' or set RECONC_INSTALL_DIR=~/bin."
fi

log "installed ${target}"
log "version: $("$target" --version)"
log "next: reconc --help  or  reconc setup ."
