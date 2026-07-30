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

action_refs() {
  sed -nE 's/^[[:space:]]*(-[[:space:]]+)?uses:[[:space:]]+([^[:space:]#]+).*/\2/p' "$1"
}

verify_action_pins() {
  local workflow="$1"
  local ref action revision
  local found=false
  while IFS= read -r ref; do
    [ -n "$ref" ] || continue
    found=true
    case "$ref" in
      ./*) continue ;;
    esac
    action="${ref%@*}"
    revision="${ref##*@}"
    if [ "$action" = "$ref" ]; then
      printf '%s\n' "$workflow contains an action without a revision: $ref" >&2
      return 1
    fi
    case "$action" in
      actions/checkout|actions/setup-go|actions/setup-node|actions/upload-artifact|actions/attest-build-provenance|github/codeql-action/init|github/codeql-action/analyze) ;;
      *)
        printf '%s\n' "$workflow uses an action outside the allowlist: $action" >&2
        return 1
        ;;
    esac
    if [[ ! "$revision" =~ ^[0-9a-f]{40}$ ]]; then
      printf '%s\n' "$workflow action $action is not pinned to a full commit SHA: $revision" >&2
      return 1
    fi
  done < <(action_refs "$workflow")
  if [ "$found" != true ]; then
    printf '%s\n' "$workflow contains no actions" >&2
    return 1
  fi
}

require_action() {
  local workflow="$1"
  local action="$2"
  action_refs "$workflow" | grep -Eq "^${action}@[0-9a-f]{40}$" \
    || fail "$workflow is missing required SHA-pinned action: $action"
}

verify_manual_dispatch_only() {
  local workflow="$1"
  local trigger_keys
  trigger_keys=$(
    sed -n '/^on:$/,/^[^[:space:]#][^:]*:/p' "$workflow" \
      | sed -nE 's/^  ([[:alnum:]_-]+):.*/\1/p' \
      | sort -u
  )
  if [ "$trigger_keys" != "workflow_dispatch" ]; then
    printf '%s\n' "$workflow release triggers must contain only workflow_dispatch" >&2
    return 1
  fi
}

(cd "$root" && go run ./scripts/audits/publication --root "$root") \
  || fail "publication audit failed"
(cd "$root" && go test ./scripts/audits/publication \
  -run 'TestGitHubCommunitySurfaceIsSubstantive|TestCodeQLWorkflowHasBoundedAdvancedSetup|TestCIWorkflowRunsOnCandidateRefs|TestDependabotCoversBoundedDependencySurfaces') \
  || fail "GitHub trust-surface contract failed"

version_source="$root/cmd/reconc/main.go"
project_version=$(sed -n 's/^var Version = "\([^"]*\)"/\1/p' "$version_source")
[[ "$project_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] \
  || fail "$version_source does not define exactly one stable semantic version"
release_line="v${project_version%.*}.x"
require_text "$root/Makefile" "VERSION   ?= $project_version"
require_text "$root/install.sh" "sh install.sh --channel preview"
require_text "$root/install.sh" "sh install.sh --version $project_version"
require_text "$root/install.sh" 'LC_ALL=C awk -v left="$1" -v right="$2"'
require_text "$root/install.ps1" '[ValidateSet("Stable", "Preview")]'
require_text "$root/install.ps1" '[switch]$AllowDowngrade'
require_text "$root/README.md" "The source line is \`$release_line\`, and the current source version is \`v$project_version\`."
require_text "$root/SECURITY.md" "only to the latest GitHub Release when one exists"
require_text "$root/AGENTS.md" "The current source line is \`$release_line\`; the source version is \`v$project_version\`."
require_text "$root/docs/documentation.md" "The current source line is \`$release_line\`; the source version is \`v$project_version\`."
require_text "$root/.github/releases/reconc-v$project_version.md" "# reconc v$project_version"
require_text "$root/Makefile" "publication-audit:"

ci_workflow="$root/.github/workflows/reconc-ci.yml"
release_workflow="$root/.github/workflows/reconc-release.yml"
codeql_workflow="$root/.github/workflows/codeql.yml"
dependabot_config="$root/.github/dependabot.yml"

action_fixture="$tmp/action-pins.yml"
printf '%s\n' 'steps:' '  - uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd' > "$action_fixture"
verify_action_pins "$action_fixture" || fail "valid action pin fixture failed"
printf '%s\n' 'steps:' '  - uses: actions/checkout@v7' > "$action_fixture"
expect_failure verify_action_pins "$action_fixture"
printf '%s\n' 'steps:' '  - uses: third-party/example@0123456789012345678901234567890123456789' > "$action_fixture"
expect_failure verify_action_pins "$action_fixture"
printf '%s\n' 'steps:' '  - uses: oven-sh/setup-bun@0c5077e51419868618aeaa5fe8019c62421857d6' > "$action_fixture"
expect_failure verify_action_pins "$action_fixture"

trigger_fixture="$tmp/release-trigger.yml"
printf '%s\n' 'on:' '  workflow_dispatch:' 'jobs:' > "$trigger_fixture"
verify_manual_dispatch_only "$trigger_fixture" || fail "valid manual release trigger fixture failed"
printf '%s\n' 'on:' '  workflow_dispatch:' '  push:' 'jobs:' > "$trigger_fixture"
expect_failure verify_manual_dispatch_only "$trigger_fixture"

for workflow in "$ci_workflow" "$release_workflow" "$codeql_workflow"; do
  verify_action_pins "$workflow" || fail "$workflow action trust validation failed"
  require_action "$workflow" "actions/checkout"
done
require_action "$ci_workflow" "actions/setup-go"
require_action "$release_workflow" "actions/setup-go"
require_action "$codeql_workflow" "actions/setup-go"
require_action "$codeql_workflow" "github/codeql-action/init"
require_action "$codeql_workflow" "github/codeql-action/analyze"
require_action "$ci_workflow" "actions/setup-node"
require_action "$release_workflow" "actions/setup-node"
require_action "$release_workflow" "actions/attest-build-provenance"
[ "$(grep -Fc 'uses: actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16 # v6.5.0' "$ci_workflow")" -eq 3 ] \
  || fail "$ci_workflow must provision pinned Go in all three jobs"
[ "$(grep -Fc 'go-version-file: go.mod' "$ci_workflow")" -eq 3 ] \
  || fail "$ci_workflow must derive Go from go.mod in all three jobs"
[ "$(grep -Fc 'fetch-depth: 0' "$ci_workflow")" -eq 3 ] \
  || fail "$ci_workflow must fetch full history in all three jobs"
[ "$(grep -Fc 'fetch-depth: 0' "$release_workflow")" -eq 1 ] \
  || fail "$release_workflow must fetch full history exactly once"
for workflow in "$ci_workflow" "$release_workflow"; do
  require_text "$workflow" "uses: actions/setup-node@820762786026740c76f36085b0efc47a31fe5020 # v7.0.0"
done
[ "$(grep -Fc 'node-version: 24.18.0' "$ci_workflow")" -eq 2 ] \
  || fail "$ci_workflow must provision Node.js 24.18.0 in both executable-test jobs"
[ "$(grep -Fc 'node-version: 24.18.0' "$release_workflow")" -eq 1 ] \
  || fail "$release_workflow must provision Node.js 24.18.0 exactly once"
[ "$(grep -Fc 'package-manager-cache: false' "$ci_workflow")" -eq 2 ] \
  || fail "$ci_workflow must disable implicit package-manager caching in both executable-test jobs"
[ "$(grep -Fc 'package-manager-cache: false' "$release_workflow")" -eq 1 ] \
  || fail "$release_workflow must disable implicit package-manager caching exactly once"
bun_integrity='sha512-aB6GVd42x1Y5ie1K16SF+oLGtgSkwX9hgoDdIW88pjvfTccU8F1vfpoOt34QLv0dZ1v3XimtaxPlZUG81Gx9Zg=='
for workflow in "$ci_workflow" "$release_workflow"; do
  require_text "$workflow" "BUN_VERSION: 1.3.14"
  require_text "$workflow" "BUN_INTEGRITY: $bun_integrity"
  require_text "$workflow" 'test "$actual_bun_integrity" = "$BUN_INTEGRITY"'
  if grep -Fq 'npm install --global bun@' "$workflow"; then
    fail "$workflow installs Bun directly without verifying the packed artifact"
  fi
done
[ "$(grep -Fc 'npm pack "bun@$BUN_VERSION"' "$ci_workflow")" -eq 2 ] \
  || fail "$ci_workflow must fetch the exact Bun package in both executable-test jobs"
[ "$(grep -Fc 'npm pack "bun@$BUN_VERSION"' "$release_workflow")" -eq 1 ] \
  || fail "$release_workflow must fetch the exact Bun package exactly once"
[ "$(grep -Fc 'npm install --global "$bun_package_dir/bun-$BUN_VERSION.tgz"' "$ci_workflow")" -eq 2 ] \
  || fail "$ci_workflow must install only the verified Bun tarball in both executable-test jobs"
[ "$(grep -Fc 'npm install --global "$bun_package_dir/bun-$BUN_VERSION.tgz"' "$release_workflow")" -eq 1 ] \
  || fail "$release_workflow must install only the verified Bun tarball exactly once"
[ "$(grep -Fc 'test "$(bun --version)" = "$BUN_VERSION"' "$ci_workflow")" -eq 2 ] \
  || fail "$ci_workflow must verify the exact Bun version in both executable-test jobs"
[ "$(grep -Fc 'test "$(bun --version)" = "$BUN_VERSION"' "$release_workflow")" -eq 1 ] \
  || fail "$release_workflow must verify the exact Bun version exactly once"
require_text "$release_workflow" "subject-checksums: dist/SHA256SUMS"
require_text "$release_workflow" "make publication-audit"
[ "$(grep -Fc 'make publication-audit' "$ci_workflow")" -eq 2 ] \
  || fail "$ci_workflow must run the publication audit in both executable-test jobs"
require_text "$release_workflow" "  workflow_dispatch:"
require_text "$release_workflow" "      tag:"
# shellcheck disable=SC2016 # Match workflow expressions literally.
require_text "$release_workflow" 'ref: ${{ inputs.tag }}'
verify_manual_dispatch_only "$release_workflow" \
  || fail "$release_workflow must be manual-dispatch only"
# shellcheck disable=SC2016 # Match workflow shell expressions literally.
require_text "$release_workflow" 'test "$tag_version" = "$source_version"'
# shellcheck disable=SC2016 # Match workflow shell expressions literally.
require_text "$release_workflow" 'test "$GITHUB_REF" = "refs/tags/$RELEASE_TAG"'
# shellcheck disable=SC2016 # Match workflow shell expressions literally.
require_text "$release_workflow" 'gh release upload "$RELEASE_TAG" dist/* --clobber'
for runner in ubuntu-24.04 macos-15 windows-2025; do
  require_text "$ci_workflow" "$runner"
done
require_text "$ci_workflow" "  push:"
require_text "$ci_workflow" "  pull_request:"
require_text "$ci_workflow" "  workflow_dispatch:"
if grep -Eq 'pull-requests:[[:space:]]*write|issues:[[:space:]]*write' "$ci_workflow"; then
  fail "$ci_workflow must not create or mutate pull requests or issues"
fi
[ -f "$dependabot_config" ] || fail "bounded Dependabot configuration is missing"
for workflow in "$ci_workflow" "$release_workflow"; do
  require_text "$workflow" "go test ./..."
  require_text "$workflow" "(cd harness/template && go test ./...)"
done
require_text "$ci_workflow" "go mod tidy -diff"
require_text "$ci_workflow" "govulncheck@v1.6.0"
require_text "$ci_workflow" "staticcheck@v0.7.0"
require_text "$release_workflow" "govulncheck@v1.6.0"
require_text "$ci_workflow" "make self-host"
require_text "$ci_workflow" "shell: pwsh"
require_text "$ci_workflow" "./scripts/tests/test-windows-installer.ps1"
require_text "$release_workflow" "make self-host"
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
release_commit=$(git -C "$root" rev-parse HEAD)
release_epoch=$(git -C "$root" show -s --format=%ct "$release_commit")
(
  cd "$root"
  go run ./cmd/reconc completion bash > "$release_dir/reconc.bash"
  go run ./cmd/reconc completion zsh > "$release_dir/_reconc"
  go run ./cmd/reconc completion fish > "$release_dir/reconc.fish"
  SOURCE_DATE_EPOCH="$release_epoch" go run \
    -ldflags "-X main.Version=$project_version" ./cmd/reconc manpage > "$release_dir/reconc.1"
)
release_assets=(
  _reconc
  install.ps1
  install.sh
  reconc.1
  reconc.bash
  reconc.fish
  completion-report.schema.json
  global-diagnostic.schema.json
  global-lifecycle.schema.json
  harness-pack-manifest.schema.json
  installation-receipt.schema.json
  policy-fix-plan.schema.json
  policy-config.schema.json
  policy-lock-v1.schema.json
  policy-lock-v2.schema.json
  policy-lock.schema.json
  policy-report.schema.json
  proof-bundle.schema.json
  reconc-harness-pack-advanced-1.0.0.zip
  release-manifest.schema.json
  repository-install.schema.json
  repository-sync-plan.schema.json
  repository-sync-report.schema.json
  "reconc-$project_version-darwin-amd64"
  "reconc-$project_version-darwin-arm64"
  "reconc-$project_version-linux-amd64"
  "reconc-$project_version-linux-arm64"
  "reconc-$project_version-windows-amd64.exe"
)
for name in "${release_assets[@]}"; do
  case "$name" in
    _reconc|reconc.1|reconc.bash|reconc.fish) ;;
    install.ps1|install.sh) cp "$root/$name" "$release_dir/$name" ;;
    policy-lock-v1.schema.json) cp "$root/schemas/v1/policy-lock.schema.json" "$release_dir/$name" ;;
    policy-lock-v2.schema.json) cp "$root/schemas/v2/policy-lock.schema.json" "$release_dir/$name" ;;
    policy-lock.schema.json) cp "$root/schemas/v3/policy-lock.schema.json" "$release_dir/$name" ;;
    *.schema.json) cp "$root/schemas/v1/$name" "$release_dir/$name" ;;
    reconc-harness-pack-advanced-1.0.0.zip) cp "$root/harness/advanced-pack.zip" "$release_dir/$name" ;;
    *) printf '%s\n' "$name" > "$release_dir/$name" ;;
  esac
done
generate_sbom() {
  (
    cd "$root"
    go run ./scripts/release/sbom generate \
      --root "$root" \
      --output-dir "$1" \
      --version "$2" \
      --commit "$release_commit" \
      --source-date-epoch "$release_epoch"
  )
}
generate_sbom "$release_dir" "$project_version"
"$root/scripts/release/write-checksums.sh" "$release_dir"
verify_release=("$root/scripts/release/verify-artifacts.sh" "$release_dir" reconc "$project_version" darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64)
"${verify_release[@]}"
printf '\n# stale despite a valid checksum\n' >> "$release_dir/install.ps1"
"$root/scripts/release/write-checksums.sh" "$release_dir"
expect_failure "${verify_release[@]}"
cp "$root/install.ps1" "$release_dir/install.ps1"
"$root/scripts/release/write-checksums.sh" "$release_dir"
"${verify_release[@]}"
printf '\n' >> "$release_dir/completion-report.schema.json"
"$root/scripts/release/write-checksums.sh" "$release_dir"
expect_failure "${verify_release[@]}"
cp "$root/schemas/v1/completion-report.schema.json" "$release_dir/completion-report.schema.json"
"$root/scripts/release/write-checksums.sh" "$release_dir"
"${verify_release[@]}"
printf '\n# stale despite a valid checksum\n' >> "$release_dir/reconc.bash"
"$root/scripts/release/write-checksums.sh" "$release_dir"
expect_failure "${verify_release[@]}"
(cd "$root" && go run ./cmd/reconc completion bash > "$release_dir/reconc.bash")
"$root/scripts/release/write-checksums.sh" "$release_dir"
"${verify_release[@]}"
printf 'corrupt\n' >> "$release_dir/reconc-$project_version-linux-amd64"
expect_failure "${verify_release[@]}"
printf '%s\n' "reconc-$project_version-linux-amd64" > "$release_dir/reconc-$project_version-linux-amd64"
printf 'unlisted\n' > "$release_dir/unlisted"
expect_failure "${verify_release[@]}"
rm "$release_dir/unlisted"

spdx="$release_dir/reconc-$project_version.spdx.json"
cyclonedx="$release_dir/reconc-$project_version.cdx.json"
mv "$spdx" "$spdx.missing"
expect_failure "${verify_release[@]}"
mv "$spdx.missing" "$spdx"

duplicate_manifest_line=$(head -n 1 "$release_dir/SHA256SUMS")
printf '%s\n' "$duplicate_manifest_line" >> "$release_dir/SHA256SUMS"
expect_failure "${verify_release[@]}"
"$root/scripts/release/write-checksums.sh" "$release_dir"

printf '{\n' >> "$cyclonedx"
"$root/scripts/release/write-checksums.sh" "$release_dir"
expect_failure "${verify_release[@]}"
generate_sbom "$release_dir" "$project_version"
"$root/scripts/release/write-checksums.sh" "$release_dir"

stale_dir="$tmp/stale-sbom"
generate_sbom "$stale_dir" "9.8.7"
cp "$stale_dir/reconc-9.8.7.spdx.json" "$spdx"
cp "$stale_dir/reconc-9.8.7.cdx.json" "$cyclonedx"
"$root/scripts/release/write-checksums.sh" "$release_dir"
expect_failure "${verify_release[@]}"
generate_sbom "$release_dir" "$project_version"
"$root/scripts/release/write-checksums.sh" "$release_dir"

grep -v "  reconc-$project_version.spdx.json$" "$release_dir/SHA256SUMS" > "$release_dir/SHA256SUMS.filtered"
mv "$release_dir/SHA256SUMS.filtered" "$release_dir/SHA256SUMS"
expect_failure "${verify_release[@]}"

rm "$release_dir/SHA256SUMS"
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
asset="reconc-${project_version}-${os}-${arch}"

(cd "$root" && make --no-print-directory build)
cp "$root/.build/bin/reconc" "$fixture/$asset"
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
    RECONC_ATTESTATION_TOOL="${RECONC_TEST_ATTESTATION_TOOL:-reconc-attestation-absent}" \
    RECONC_REQUIRE_ATTESTATION="${RECONC_TEST_REQUIRE_ATTESTATION:-0}" \
    sh "$root/install.sh" "$project_version"
}

expect_failure sh "$root/install.sh" --channel stable --version "$project_version"
expect_failure sh "$root/install.sh" --version "$project_version-preview.01"
sh "$root/install.sh" --help >/dev/null

printf '#!/usr/bin/env sh\nprintf "old\\n"\n' > "$install_dir/reconc"
chmod +x "$install_dir/reconc"
installer_output=$(run_installer 2>&1)
[ "$("$install_dir/reconc" --version)" = "reconc ${project_version}" ] \
  || fail "verified installer did not publish the downloaded binary"
printf '%s\n' "$installer_output" | grep -Fq 'PATH: add this line to your shell profile' \
  || fail "POSIX installer did not report the missing PATH activation"

receipt_home="$tmp/reconc-home"
if ! receipt_install_output=$(
  PATH="$fake_bin:$install_dir:$PATH" \
    RECONC_HOME="$receipt_home" \
    RECONC_TEST_FIXTURE="$fixture" \
    RECONC_RELEASE_BASE="https://release.invalid" \
    RECONC_INSTALL_DIR="$install_dir" \
    RECONC_ATTESTATION_TOOL="reconc-attestation-absent" \
    RECONC_REQUIRE_ATTESTATION=0 \
    sh "$root/install.sh" "$project_version" 2>&1
); then
  fail "PATH-ready POSIX installer failed: $receipt_install_output"
fi
[ -f "$receipt_home/install/receipt.json" ] \
  || fail "PATH-ready POSIX installer did not publish an ownership receipt"
global_report=$(
  PATH="$install_dir:$PATH" RECONC_HOME="$receipt_home" \
    "$install_dir/reconc" doctor --global --json
)
printf '%s\n' "$global_report" | grep -Fq '"status": "healthy"' \
  || fail "PATH-ready POSIX install is not globally healthy"
printf '%s\n' "$global_report" | grep -Fq '"owner": "direct"' \
  || fail "POSIX installer did not retain direct ownership"
printf '%s\n' "$global_report" | grep -Fq '"channel": "exact"' \
  || fail "POSIX installer did not retain the exact channel"

installed_digest=$(sha256_file "$install_dir/reconc")
dd if=/dev/zero of="$fixture/SHA256SUMS" bs=1048576 count=3 2>/dev/null
expect_failure run_installer
[ "$(sha256_file "$install_dir/reconc")" = "$installed_digest" ] \
  || fail "oversized checksum download replaced the installed binary"
printf '%s  %s\n' "$(sha256_file "$fixture/$asset")" "$asset" > "$fixture/SHA256SUMS"

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

# --- Attestation verification paths -----------------------------------

# Restore a healthy fixture for the attestation cases.
cp "$root/.build/bin/reconc" "$fixture/$asset"
printf '%s  %s\n' "$(sha256_file "$fixture/$asset")" "$asset" > "$fixture/SHA256SUMS"

# Required attestation with the tool absent must fail before install.
printf '#!/usr/bin/env sh\nprintf "sentinel\\n"\n' > "$install_dir/reconc"
chmod +x "$install_dir/reconc"
RECONC_TEST_REQUIRE_ATTESTATION=1 expect_failure run_installer
[ "$("$install_dir/reconc")" = "sentinel" ] \
  || fail "required attestation without gh replaced the installed binary"

# A succeeding attestation tool lets the install proceed.
cat > "$fake_bin/reconc-attestation-pass" <<'SCRIPT'
#!/usr/bin/env sh
[ "${1:-}" = "attestation" ] && [ "${2:-}" = "verify" ] || exit 2
exit 0
SCRIPT
chmod +x "$fake_bin/reconc-attestation-pass"
RECONC_TEST_ATTESTATION_TOOL=reconc-attestation-pass \
  RECONC_TEST_REQUIRE_ATTESTATION=1 run_installer >/dev/null 2>&1
[ "$("$install_dir/reconc" --version)" = "reconc ${project_version}" ] \
  || fail "verified attestation did not publish the downloaded binary"

# A failing attestation tool blocks the install when required...
cat > "$fake_bin/reconc-attestation-fail" <<'SCRIPT'
#!/usr/bin/env sh
exit 1
SCRIPT
chmod +x "$fake_bin/reconc-attestation-fail"
printf '#!/usr/bin/env sh\nprintf "sentinel\\n"\n' > "$install_dir/reconc"
chmod +x "$install_dir/reconc"
RECONC_TEST_ATTESTATION_TOOL=reconc-attestation-fail \
  RECONC_TEST_REQUIRE_ATTESTATION=1 expect_failure run_installer
[ "$("$install_dir/reconc")" = "sentinel" ] \
  || fail "failed required attestation replaced the installed binary"

# ...and only warns when not required.
RECONC_TEST_ATTESTATION_TOOL=reconc-attestation-fail run_installer >/dev/null 2>&1
[ "$("$install_dir/reconc" --version)" = "reconc ${project_version}" ] \
  || fail "optional failed attestation must not block the install"

printf 'release-trust: ok\n'
