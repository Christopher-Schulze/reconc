package hooks

// WrapperPath is the canonical repository-local hook launcher. Agent hook
// artifacts call this stable path and leave binary discovery to one small
// resolver.
const WrapperPath = "tools/reconc/bin/hook"

// GenerateWrapper returns the version-independent repository-local hook
// launcher. A development binary wins without OS/architecture subprocesses;
// otherwise a stable platform artifact wins. Exactly one compatible versioned
// release artifact is accepted as a migration fallback; multiple matches fail
// closed instead of selecting a version by directory order.
func GenerateWrapper() *Artifact {
	content := `#!/bin/sh
# Managed by Reconc. Repo-local agent hook wrapper.
#
# Usage: tools/reconc/bin/hook <event> [repo]
# Reads the agent hook JSON payload from stdin and execs the best available
# repo-local Reconc binary. The final exec keeps hook processes single-layered
# so shells do not remain as idle parents after agent events.

set -eu

event="${1:-}"
repo="${2:-}"

if [ -z "$event" ]; then
  echo "reconc hook wrapper: missing event" >&2
  exit 1
fi

if [ -z "$repo" ]; then
  repo="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
fi

if [ "${RECONC_HOOK_REPO_RESOLVED:-}" != "1" ] && [ ! -x "$repo/tools/reconc/bin/hook" ]; then
  repo="$(git -C "$repo" rev-parse --show-toplevel 2>/dev/null || printf "%s" "$repo")"
fi

grok_fail_closed() {
  printf '%s\n' '{"decision":"deny","reason":"Reconc could not evaluate this Grok tool call. Repair the Reconc binary or hook installation before retrying."}'
  exit 0
}

grok_decision_valid() {
  case "$1" in
    *'
'*) return 1 ;;
  esac
  if printf '%s' "$1" | LC_ALL=C grep -q '[[:cntrl:]]'; then
    return 1
  fi
  printf '%s\n' "$1" | grep -Eq '^\{"decision":"allow"\}$|^\{"decision":"deny","reason":"([^"\\]|\\(["\\/bfnrt]|u[0-9A-Fa-f]{4}))*"\}$'
}

run_reconc_hook() {
  reconc_binary="$1"
  if [ "$event" = "grok-pre-tool-use" ]; then
    set +e
    grok_output=$("$reconc_binary" hook grok-pre-tool-guard "$repo")
    grok_status=$?
    set -e
    if [ "$grok_status" -ne 0 ] || [ -z "$grok_output" ]; then
      grok_fail_closed
    fi
    if ! grok_decision_valid "$grok_output"; then
      grok_fail_closed
    fi
    printf '%s\n' "$grok_output"
    exit 0
  fi
  exec "$reconc_binary" hook runtime "$event" "$repo"
}

for dev_reconc in "$repo/.build/bin/reconc" "$repo/reconc"; do
  if [ -x "$dev_reconc" ]; then
    run_reconc_hook "$dev_reconc"
  fi
done

` + shellBinaryResolver() + `
for reconc_dir in "$repo/tools/reconc/dist" "$repo/dist"; do
  resolve_status=0
  resolve_reconc_dir "$reconc_dir" || resolve_status=$?
  if [ "$resolve_status" -eq 0 ]; then
    run_reconc_hook "$resolved_reconc"
  fi
  if [ "$resolve_status" -eq 2 ]; then
    if [ "$event" = "grok-pre-tool-use" ]; then
      grok_fail_closed
    fi
    exit 2
  fi
done

if command -v reconc >/dev/null 2>&1; then
  run_reconc_hook "$(command -v reconc)"
fi

if [ "$event" = "grok-pre-tool-use" ]; then
  grok_fail_closed
fi
echo "reconc hook wrapper: no executable Reconc binary found" >&2
echo "expected one stable or unambiguous versioned repo-local binary, a dev binary, or reconc on PATH" >&2
exit 2
`
	return &Artifact{Kind: "hook-wrapper", TargetPath: WrapperPath, Executable: true, Content: content}
}

func shellBinaryResolver() string {
	return `case "$(uname -s)" in
  Darwin) reconc_os="darwin" ;;
  Linux) reconc_os="linux" ;;
  CYGWIN*|MINGW*|MSYS*) reconc_os="windows" ;;
  *) reconc_os="" ;;
esac

case "$(uname -m)" in
  arm64|aarch64) reconc_arch="arm64" ;;
  x86_64|amd64) reconc_arch="amd64" ;;
  *) reconc_arch="" ;;
esac

reconc_ext=""
if [ "$reconc_os" = "windows" ]; then
  reconc_ext=".exe"
fi

resolve_reconc_dir() {
  resolved_reconc=""
  if [ -z "$reconc_os" ] || [ -z "$reconc_arch" ]; then
    return 1
  fi
  reconc_dir="$1"
  stable_reconc="$reconc_dir/reconc-$reconc_os-$reconc_arch$reconc_ext"
  if [ -x "$stable_reconc" ]; then
    resolved_reconc="$stable_reconc"
    return 0
  fi
  reconc_matches=0
  for reconc_candidate in "$reconc_dir"/reconc-*-"${reconc_os}-${reconc_arch}${reconc_ext}"; do
    if [ ! -x "$reconc_candidate" ]; then
      continue
    fi
    reconc_matches=$((reconc_matches + 1))
    resolved_reconc="$reconc_candidate"
  done
  if [ "$reconc_matches" -gt 1 ]; then
    echo "reconc binary resolution is ambiguous under $reconc_dir for $reconc_os/$reconc_arch" >&2
    echo "install the stable reconc-$reconc_os-$reconc_arch$reconc_ext artifact or keep exactly one versioned candidate" >&2
    return 2
  fi
  [ "$reconc_matches" -eq 1 ]
}
`
}
