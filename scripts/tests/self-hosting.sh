#!/usr/bin/env bash

set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
binary=${RECONC_BIN:-"$root/.build/bin/reconc"}
tmp=$(mktemp -d "${TMPDIR:-/tmp}/reconc-self-host.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT HUP TERM

fail() {
  printf 'self-hosting: %s\n' "$1" >&2
  exit 1
}

require_text() {
  file=$1
  value=$2
  grep -Fq -- "$value" "$file" || fail "$file is missing: $value"
}

run_json() {
  output=$1
  shift
  "$@" --json >"$output"
}

[ -x "$binary" ] || fail "Reconc binary is missing or not executable: $binary"
binary_dir=$(cd "$(dirname "$binary")" && pwd)
git -C "$root" check-ignore --no-index --quiet .reconc/run/decisions.jsonl.lock \
  || fail "self-hosted repository does not ignore Reconc run runtime"

mkdir -p "$tmp/bin" "$tmp/reconc-home" "$tmp/runtime-tmp" "$tmp/state"
export RECONC_HOME="$tmp/reconc-home"
export RECONC_INSTALL_DIR="$tmp/bin"
export PATH="$RECONC_INSTALL_DIR:$binary_dir:$PATH"
export TMPDIR="$tmp/runtime-tmp"
export RECONC_CLAUDE_STATE_DIR="$tmp/state"
export RECONC_AUDIT=1

minimal="$tmp/minimal"
mkdir -p "$minimal"
run_json "$tmp/minimal-plan.json" "$binary" bootstrap plan "$minimal" --profile minimal
run_json "$tmp/minimal-apply.json" "$binary" bootstrap apply --plan "$tmp/minimal-plan.json"
run_json "$tmp/minimal-verify.json" "$binary" bootstrap verify --plan "$tmp/minimal-plan.json"
require_text "$tmp/minimal-apply.json" '"status": "complete"'
require_text "$tmp/minimal-verify.json" '"valid": true'
run_json "$tmp/global-doctor.json" "$RECONC_INSTALL_DIR/reconc" doctor --global
require_text "$tmp/global-doctor.json" '"status": "healthy"'
require_text "$tmp/global-doctor.json" '"owner": "source"'
run_json "$tmp/minimal-second-plan.json" "$binary" bootstrap plan "$minimal" --profile minimal
if grep -Fq '"state": "create"' "$tmp/minimal-second-plan.json" || grep -Fq '"state": "conflict"' "$tmp/minimal-second-plan.json"; then
  fail "minimal profile is not idempotent"
fi

governed="$tmp/governed"
mkdir -p "$governed"
git -C "$governed" init --quiet
git -C "$governed" config user.email reconc@example.invalid
git -C "$governed" config user.name "Reconc self-host"
printf 'module example.invalid/golden\n\ngo 1.25.0\n' >"$governed/go.mod"

run_json "$tmp/governed-plan.json" "$binary" bootstrap plan "$governed" \
  --profile governed --pack go-assurance --hook all --install-binary
run_json "$tmp/governed-apply.json" "$binary" bootstrap apply --plan "$tmp/governed-plan.json"
run_json "$tmp/governed-verify.json" "$binary" bootstrap verify --plan "$tmp/governed-plan.json"
require_text "$tmp/governed-apply.json" '"status": "complete"'
require_text "$tmp/governed-verify.json" '"valid": true'
require_text "$governed/.gitignore" '/tools/reconc/dist/'

run_json "$tmp/inspect.json" "$binary" bootstrap inspect "$governed"
require_text "$tmp/inspect.json" '"source": "tools-reconc-dist"'
stable_binary=$(find "$governed/tools/reconc/dist" -maxdepth 1 -type f -name 'reconc-*' -print)
[ "$(printf '%s\n' "$stable_binary" | grep -c .)" -eq 1 ] || fail "stable release-layout binary is ambiguous"
"$stable_binary" version >"$tmp/stable-version.txt"
"$binary" version >"$tmp/source-version.txt"
cmp -s "$tmp/source-version.txt" "$tmp/stable-version.txt" \
  || fail "stable release-layout binary version differs from the source binary"

run_json "$tmp/hook-status.json" "$stable_binary" hook status "$governed"
[ "$(grep -c '"state": "configured"' "$tmp/hook-status.json")" -eq 10 ] || fail "not all ten hook platforms are configured"

wrapper="$governed/tools/reconc/bin/hook"
for event in \
  claude-session-start \
  codex-session-start \
  copilot-session-start \
  cursor-session-start \
  opencode-session-start \
  devin-session-start \
  antigravity-pre-invocation \
  kilo-session-start \
  grok-session-start
do
  session=${event%%-*}
  if [ "$event" = "copilot-session-start" ]; then
    printf '{"hook_event_name":"SessionStart","session_id":"golden-copilot","cwd":"%s"}\n' "$governed" \
      | "$wrapper" "$event" "$governed" >"$tmp/hook-$session.json"
  elif [ "$event" = "grok-session-start" ]; then
    printf '{"hookEventName":"session_start","sessionId":"golden-grok","workspaceRoot":"%s"}\n' "$governed" \
      | "$wrapper" "$event" "$governed" >"$tmp/hook-$session.json"
  else
    printf '{"session_id":"golden-%s","reconc_runtime":"%s"}\n' "$session" "$session" \
      | "$wrapper" "$event" "$governed" >"$tmp/hook-$session.json"
  fi
done
git -C "$governed" add -A
(cd "$governed" && .git/hooks/pre-commit) >"$tmp/git-pre-commit.txt"
governed_root=$(git -C "$governed" rev-parse --show-toplevel)
require_text "$tmp/git-pre-commit.txt" "Repo:      $governed_root"

run_json "$tmp/task-status.json" "$stable_binary" task status "$governed"
run_json "$tmp/task-validate.json" "$stable_binary" task validate "$governed"
run_json "$tmp/task-block.json" "$stable_binary" task block "$governed" --reason "golden recovery proof"
run_json "$tmp/task-resume.json" "$stable_binary" task resume 001 "$governed"
require_text "$tmp/task-block.json" '"state": "blocked"'
require_text "$tmp/task-resume.json" '"state": "active"'

task_detail="$governed/docs/tasks/001-bootstrap-reconc.md"
sed -E 's/^- \[[~ ]\]/- [x]/' "$task_detail" >"$task_detail.reconc-self-host"
[ "$(grep -c '^- \[x\]' "$task_detail.reconc-self-host")" -eq 4 ] || fail "TASK completion fixture did not close all Sub-Tasks"
mv "$task_detail.reconc-self-host" "$task_detail"
run_json "$tmp/task-done.json" "$stable_binary" task check-done "$governed"
run_json "$tmp/task-archive.json" "$stable_binary" task archive "$governed"
run_json "$tmp/task-final-validate.json" "$stable_binary" task validate "$governed"
require_text "$tmp/task-archive.json" '"state": "done"'
[ -f "$governed/docs/tasks/done/001-bootstrap-reconc.md" ] || fail "TASK detail was not archived"

run_json "$tmp/check.json" "$stable_binary" check "$governed"
run_json "$tmp/doctor.json" "$stable_binary" doctor "$governed" --deep
run_json "$tmp/prune-dry.json" "$stable_binary" prune "$governed" --dry-run
run_json "$tmp/prune.json" "$stable_binary" prune "$governed"
require_text "$tmp/doctor.json" '"status": "OK"'
require_text "$tmp/prune.json" '"ran": true'

run_json "$tmp/existing-plan.json" "$binary" bootstrap plan "$governed" \
  --profile existing --hook all --install-binary
run_json "$tmp/existing-apply.json" "$binary" bootstrap apply --plan "$tmp/existing-plan.json"
run_json "$tmp/existing-verify.json" "$binary" bootstrap verify --plan "$tmp/existing-plan.json"
require_text "$tmp/existing-apply.json" '"created": []'
require_text "$tmp/existing-verify.json" '"valid": true'

if find "$tmp/runtime-tmp" -mindepth 1 -maxdepth 1 -print -quit | grep -q .; then
  fail "owned temporary residue escaped cleanup"
fi

printf 'self-hosting: ok\n'
