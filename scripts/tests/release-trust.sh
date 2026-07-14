#!/usr/bin/env bash

set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/reconc-release-trust.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT HUP TERM

fail() {
  printf 'release-trust: %s\n' "$1" >&2
  exit 1
}

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    sha256sum "$1" | awk '{print $1}'
  fi
}

expect_failure() {
  if "$@" >/dev/null 2>&1; then
    fail "command unexpectedly succeeded: $*"
  fi
}

require_text() {
  file="$1"
  value="$2"
  grep -Fq -- "$value" "$file" || fail "$file is missing required release-trust text: $value"
}

ci_workflow="$root/.github/workflows/reconc-ci.yml"
release_workflow="$root/.github/workflows/reconc-release.yml"
for workflow in "$ci_workflow" "$release_workflow"; do
  if grep -Eq 'uses:[[:space:]]+[^[:space:]]+@v[0-9]' "$workflow"; then
    fail "$workflow contains a floating action version"
  fi
  require_text "$workflow" "actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd"
done
require_text "$ci_workflow" "actions/setup-go@4a3601121dd01d1626a1e23e37211e3254c1c06c"
require_text "$release_workflow" "actions/setup-go@4a3601121dd01d1626a1e23e37211e3254c1c06c"
for runner in ubuntu-24.04 macos-15 windows-2025; do
  require_text "$ci_workflow" "$runner"
done
for workflow in "$ci_workflow" "$release_workflow"; do
  require_text "$workflow" "go test ./..."
  require_text "$workflow" "(cd harness/template && go test ./...)"
done
require_text "$ci_workflow" "go mod tidy -diff"
require_text "$ci_workflow" "staticcheck@v0.7.0"
require_text "$ci_workflow" "./scripts/tests/self-hosting.sh"
require_text "$root/scripts/tests/self-hosting.sh" "--profile governed"
require_text "$root/scripts/tests/self-hosting.sh" "--profile existing"
require_text "$root/scripts/tests/self-hosting.sh" "--hook all"
if grep -Fq 'staticcheck@latest' "$ci_workflow"; then
  fail "$ci_workflow uses an unpinned staticcheck version"
fi
draft_line=$(grep -n -- '--draft' "$release_workflow" | head -n 1 | cut -d: -f1)
upload_line=$(grep -n 'gh release upload' "$release_workflow" | head -n 1 | cut -d: -f1)
publish_line=$(grep -n 'gh release edit.*--draft=false' "$release_workflow" | head -n 1 | cut -d: -f1)
[ "$draft_line" -lt "$upload_line" ] && [ "$upload_line" -lt "$publish_line" ] \
  || fail "release workflow must remain draft until every verified artifact is uploaded"

release_dir="$tmp/release"
mkdir -p "$release_dir"
expect_failure "$root/scripts/release/write-checksums.sh" "$release_dir"
release_assets=(
  _reconc
  reconc.1
  reconc.bash
  reconc.fish
  policy-fix-plan.schema.json
  policy-lock.schema.json
  policy-report.schema.json
  reconc-0.6.0-darwin-amd64
  reconc-0.6.0-darwin-arm64
  reconc-0.6.0-linux-amd64
  reconc-0.6.0-linux-arm64
  reconc-0.6.0-windows-amd64.exe
)
for name in "${release_assets[@]}"; do
  printf '%s\n' "$name" > "$release_dir/$name"
done
"$root/scripts/release/write-checksums.sh" "$release_dir"
verify_release=("$root/scripts/release/verify-artifacts.sh" "$release_dir" reconc 0.6.0 darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64)
"${verify_release[@]}"
printf 'corrupt\n' >> "$release_dir/reconc-0.6.0-linux-amd64"
expect_failure "${verify_release[@]}"
printf '%s\n' reconc-0.6.0-linux-amd64 > "$release_dir/reconc-0.6.0-linux-amd64"
printf 'unlisted\n' > "$release_dir/unlisted"
expect_failure "${verify_release[@]}"
rm "$release_dir/unlisted" "$release_dir/SHA256SUMS"
mkdir -p "$tmp/broken-hash-bin"
cat > "$tmp/broken-hash-bin/shasum" <<'SCRIPT'
#!/usr/bin/env sh
exit 9
SCRIPT
chmod +x "$tmp/broken-hash-bin/shasum"
expect_failure env PATH="$tmp/broken-hash-bin:$PATH" "$root/scripts/release/write-checksums.sh" "$release_dir"
[ ! -e "$release_dir/SHA256SUMS" ] || fail "checksum failure published a manifest"

make_dir="$tmp/make-release"
mkdir -p "$make_dir"
cp "$root/Makefile" "$make_dir/Makefile"
cat > "$make_dir/fail-go" <<'SCRIPT'
#!/usr/bin/env sh
printf '%s\n' "$*" >> "$GO_CALL_LOG"
exit 23
SCRIPT
chmod +x "$make_dir/fail-go"
if (cd "$make_dir" && GO_CALL_LOG="$make_dir/go-calls" make release GO="$make_dir/fail-go" VERSION=9.9.9) >/dev/null 2>&1; then
  fail "release target hid a failed build"
fi
[ "$(wc -l < "$make_dir/go-calls" | tr -d ' ')" -eq 1 ] \
  || fail "release target continued after its first failed build"

fixture="$tmp/installer-fixture"
fake_bin="$tmp/fake-bin"
install_dir="$tmp/install"
mkdir -p "$fixture" "$fake_bin" "$install_dir"

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) fail "installer test does not support this host OS" ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) fail "installer test does not support this host architecture" ;;
esac
asset="reconc-0.6.0-${os}-${arch}"

cat > "$fixture/$asset" <<'SCRIPT'
#!/usr/bin/env sh
[ "${1:-}" = "--version" ] || exit 2
printf 'reconc 0.6.0-test\n'
SCRIPT
chmod +x "$fixture/$asset"
printf '%s  %s\n' "$(sha256_file "$fixture/$asset")" "$asset" > "$fixture/SHA256SUMS"

cat > "$fake_bin/curl" <<'SCRIPT'
#!/usr/bin/env sh
set -eu
destination=""
url=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o)
      destination="$2"
      shift 2
      ;;
    *)
      url="$1"
      shift
      ;;
  esac
done
[ -n "$destination" ] && [ -n "$url" ]
cp "$RECONC_TEST_FIXTURE/${url##*/}" "$destination"
SCRIPT
chmod +x "$fake_bin/curl"

run_installer() {
  PATH="$fake_bin:$PATH" \
    RECONC_TEST_FIXTURE="$fixture" \
    RECONC_RELEASE_BASE="https://release.invalid" \
    RECONC_INSTALL_DIR="$install_dir" \
    sh "$root/install.sh" 0.6.0
}

printf '#!/usr/bin/env sh\nprintf "old\\n"\n' > "$install_dir/reconc"
chmod +x "$install_dir/reconc"
run_installer >/dev/null 2>&1
[ "$("$install_dir/reconc" --version)" = "reconc 0.6.0-test" ] \
  || fail "verified installer did not publish the downloaded binary"

printf '#!/usr/bin/env sh\nprintf "sentinel\\n"\n' > "$install_dir/reconc"
chmod +x "$install_dir/reconc"
printf '%064d  %s\n' 0 "$asset" > "$fixture/SHA256SUMS"
expect_failure run_installer
[ "$("$install_dir/reconc")" = "sentinel" ] \
  || fail "checksum failure replaced the installed binary"

printf '%s  other-asset\n' "$(sha256_file "$fixture/$asset")" > "$fixture/SHA256SUMS"
expect_failure run_installer
[ "$("$install_dir/reconc")" = "sentinel" ] \
  || fail "missing manifest entry replaced the installed binary"

hash=$(sha256_file "$fixture/$asset")
printf '%s  %s\n%s  %s\n' "$hash" "$asset" "$hash" "$asset" > "$fixture/SHA256SUMS"
expect_failure run_installer
[ "$("$install_dir/reconc")" = "sentinel" ] \
  || fail "duplicate manifest entry replaced the installed binary"

printf '#!/usr/bin/env sh\nexit 17\n' > "$fixture/$asset"
chmod +x "$fixture/$asset"
printf '%s  %s\n' "$(sha256_file "$fixture/$asset")" "$asset" > "$fixture/SHA256SUMS"
expect_failure run_installer
[ "$("$install_dir/reconc")" = "sentinel" ] \
  || fail "non-executable release payload replaced the installed binary"

printf 'release-trust: ok\n'
