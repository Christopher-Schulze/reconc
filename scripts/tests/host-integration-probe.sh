#!/usr/bin/env bash
set -euo pipefail

usage() {
  printf '%s\n' \
    'Usage: scripts/tests/host-integration-probe.sh --host HOST --surface SURFACE [--allow-authenticated] [--json]' \
    'Hosts/surfaces: cursor desktop-agent|desktop-cmd-k|tab|cli-interactive|cli-print|cloud; opencode cli; kilo cli|vscode'
}

host=''
surface=''
allow_authenticated=false
json_output=false
while (($# > 0)); do
  case "$1" in
    --host)
      (($# >= 2)) || { usage >&2; exit 2; }
      host="$2"
      shift 2
      ;;
    --surface)
      (($# >= 2)) || { usage >&2; exit 2; }
      surface="$2"
      shift 2
      ;;
    --allow-authenticated)
      allow_authenticated=true
      shift
      ;;
    --json)
      json_output=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      printf 'Unknown flag: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

case "$host:$surface" in
  cursor:desktop-agent|cursor:desktop-cmd-k|cursor:tab|cursor:cli-interactive|cursor:cli-print|cursor:cloud|opencode:cli|kilo:cli|kilo:vscode) ;;
  *)
    printf 'Unsupported host/surface: %s/%s\n' "$host" "$surface" >&2
    usage >&2
    exit 2
    ;;
esac

for command_name in git go jq; do
  command -v "$command_name" >/dev/null 2>&1 || {
    printf 'Required command is unavailable: %s\n' "$command_name" >&2
    exit 2
  }
done

case "$host" in
  cursor)
    case "$surface" in
      cli-interactive|cli-print)
        host_command="$(command -v cursor-agent || true)"
        ;;
      desktop-agent|desktop-cmd-k|tab)
        if [[ -x /Applications/Cursor.app/Contents/Resources/app/bin/cursor ]]; then
          host_command='/Applications/Cursor.app/Contents/Resources/app/bin/cursor'
        else
          host_command="$(command -v cursor || true)"
        fi
        ;;
      cloud)
        host_command=''
        ;;
    esac
    ;;
  opencode)
    host_command="$(command -v opencode || true)"
    ;;
  kilo)
    host_command="$(command -v kilo || command -v kilocode || true)"
    ;;
esac

probe_root="$(mktemp -d "${TMPDIR:-/tmp}/reconc-host-probe.XXXXXX")"
cleanup() {
  case "$probe_root" in
    "${TMPDIR:-/tmp}"/reconc-host-probe.*|/tmp/reconc-host-probe.*|/private/tmp/reconc-host-probe.*)
      rm -rf -- "$probe_root"
      ;;
  esac
}
trap cleanup EXIT INT TERM

probe_repo="$probe_root/repo"
mkdir -p "$probe_repo"
git -C "$probe_repo" init -q
printf '# Disposable Reconc host probe\n' >"$probe_repo/AGENTS.md"
printf '%s\n' \
  'default_mode: block' \
  'mcp:' \
  '  unclassified: deny' \
  '  tools: []' \
  'rules:' \
  '  - id: probe-deny-write' \
  '    kind: deny_write' \
  '    paths: [forbidden.txt]' \
  '    message: The disposable host probe must block this write.' \
  '  - id: probe-deny-command' \
  '    kind: forbid_command' \
  '    commands: ["touch forbidden-command-marker"]' \
  '    message: The disposable host probe must block this command.' \
  >"$probe_repo/.reconc.yml"
go build -o "$probe_root/reconc" ./cmd/reconc
"$probe_root/reconc" compile "$probe_repo" >/dev/null
"$probe_root/reconc" hook install "$host" "$probe_repo" --force >/dev/null
status_json="$("$probe_root/reconc" hook status "$probe_repo" --json)"
host_status="$(jq -c --arg host "$host" '.[] | select(.kind == $host)' <<<"$status_json")"
[[ -n "$host_status" ]] || {
  printf 'Generated hook status has no %s entry\n' "$host" >&2
  exit 1
}

configured="$(jq -r '.configured' <<<"$host_status")"
expected_events="$(jq -c '.expected_events // []' <<<"$host_status")"
surface_expected_events="$expected_events"
case "$host:$surface" in
  cursor:desktop-agent|cursor:desktop-cmd-k|cursor:cli-interactive|cursor:cli-print)
    surface_expected_events="$(jq -c '[.[] | select(. != "cursor-after-tab-file-edit")]' <<<"$expected_events")"
    ;;
  cursor:tab)
    surface_expected_events="$(jq -c '[.[] | select(. == "cursor-after-tab-file-edit")]' <<<"$expected_events")"
    ;;
  cursor:cloud)
    surface_expected_events="$(jq -c '[.[] | select(
      . != "cursor-session-start" and
      . != "cursor-session-end" and
      . != "cursor-before-mcp-execution" and
      . != "cursor-after-mcp-execution" and
      . != "cursor-after-tab-file-edit"
    )]' <<<"$expected_events")"
    ;;
esac

managed_wrapper="$probe_repo/tools/reconc/bin/hook"
probe_binary="$probe_repo/tools/reconc/bin/reconc-host-probe"
event_log="$probe_repo/.reconc/host-probe-events.jsonl"
raw_dir="$probe_repo/.reconc/host-probe-raw"
mkdir -p "$raw_dir" "$(dirname "$managed_wrapper")"
cp "$probe_root/reconc" "$probe_binary"
chmod 0755 "$probe_binary"
printf '%s\n' \
  '#!/bin/sh' \
  'set -u' \
  'script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)' \
  'probe_binary="$script_dir/reconc-host-probe"' \
  'repo_root=$(CDPATH= cd -- "$script_dir/../../.." && pwd)' \
  'event_log="$repo_root/.reconc/host-probe-events.jsonl"' \
  'raw_dir="$repo_root/.reconc/host-probe-raw"' \
  'mkdir -p "$raw_dir"' \
  'payload_file=$(mktemp "$raw_dir/payload.XXXXXX") || exit 1' \
  'trap '"'"'rm -f -- "$payload_file"'"'"' EXIT INT TERM' \
  'cat >"$payload_file"' \
  'fields=[]' \
  'if jq -e '"'"'type == "object"'"'"' "$payload_file" >/dev/null 2>&1; then' \
  '  fields=$(jq -c '"'"'keys | sort'"'"' "$payload_file")' \
  'fi' \
  'route=unknown' \
  'if [ "${1:-}" = hook ] && [ "${2:-}" = runtime ] && [ -n "${3:-}" ]; then' \
  '  route=$3' \
  'fi' \
  'set +e' \
  '"$probe_binary" "$@" <"$payload_file"' \
  'exit_code=$?' \
  'set -e' \
  'case "$exit_code" in' \
  '  0) outcome=allowed-or-observed ;;' \
  '  2) outcome=blocked ;;' \
  '  *) outcome=runtime-error ;;' \
  'esac' \
  'timestamp=$(date -u +%Y-%m-%dT%H:%M:%SZ)' \
  'jq -cn --arg route "$route" --arg timestamp "$timestamp" --arg outcome "$outcome" --argjson fields "$fields" --argjson exit_code "$exit_code" '"'"'{route:$route,timestamp:$timestamp,fields:$fields,outcome:$outcome,exit_code:$exit_code}'"'"' >>"$event_log"' \
  'exit "$exit_code"' \
  >"$managed_wrapper"
chmod 0755 "$managed_wrapper"

host_version=''
host_available=false
if [[ -n "$host_command" ]]; then
  host_available=true
  host_version="$("$host_command" --version 2>/dev/null | head -n 1 | tr -cd '[:print:]' | cut -c1-128)"
fi

discoverable=true
unsupported=false
degraded=true
detail='project artifact generated, installed, and statically inspected; authenticated live execution was not approved'
case "$host:$surface" in
  cursor:cloud)
    detail='project artifact is documented for Cursor cloud agents, but account access and event delivery were not authenticated or live-proven'
    ;;
  cursor:desktop-agent|cursor:desktop-cmd-k|cursor:tab|kilo:vscode)
    detail='project artifact is discoverable; this UI surface requires an approved operator-driven live probe'
    ;;
esac
if [[ "$host_available" != true ]]; then
  detail='host executable is unavailable on this machine; artifact semantics are configured but live loading is unproven'
fi

action_required=''
if [[ "$allow_authenticated" == true ]]; then
  case "$host:$surface" in
    cursor:desktop-agent)
      action_required="Open $probe_repo in Cursor Agent. Exercise prompt, permitted shell/write, failed shell, denied write to forbidden.txt, denied command touch forbidden-command-marker, MCP, subagent, compaction, and Stop. Return here and press Enter."
      ;;
    cursor:desktop-cmd-k)
      action_required="Open $probe_repo in Cursor. Exercise every documented Cmd+K trigger without attributing Agent-only events, then return here and press Enter."
      ;;
    cursor:tab)
      action_required="Open $probe_repo in Cursor, accept one Tab edit, then return here and press Enter."
      ;;
    cursor:cli-interactive)
      action_required="Run cursor-agent in $probe_repo and exercise the approved positive/negative matrix, then return here and press Enter."
      ;;
    cursor:cli-print)
      action_required="Run cursor-agent --print --output-format stream-json in $probe_repo with the approved positive/negative matrix, then return here and press Enter."
      ;;
    cursor:cloud)
      action_required="Start the approved cloud-agent probe from $probe_repo, exercise the documented cloud event set, then return here and press Enter."
      ;;
    opencode:cli)
      action_required="Run opencode $probe_repo and exercise session, permitted and denied tool, non-zero shell, compaction, idle continuation, and MCP cases, then return here and press Enter."
      ;;
    kilo:cli)
      action_required="Run kilo $probe_repo and exercise session, permitted and denied tool, non-zero shell, compaction, idle continuation, and MCP cases, then return here and press Enter."
      ;;
    kilo:vscode)
      action_required="Open $probe_repo in Kilo Code's VS Code host and exercise every documented project-plugin trigger, then return here and press Enter."
      ;;
  esac
else
  action_required='Re-run with --allow-authenticated only after explicit operator approval for model/account use.'
fi

if [[ "$allow_authenticated" == true ]]; then
  [[ -t 0 ]] || {
    printf '%s\n' 'Authenticated probes require an interactive operator terminal.' >&2
    exit 2
  }
  printf '%s\n%s\n' "Disposable probe: $probe_repo" "$action_required" >&2
  read -r -p 'Press Enter only after the approved probe is complete: ' _
fi

observed_events='[]'
loaded=false
blocked_routes='[]'
if [[ -s "$event_log" ]]; then
  observed_events="$(jq -sc 'map(.route) | unique | sort' "$event_log")"
  loaded="$(jq -s 'any(.[]; .route | test("session-start$"))' "$event_log")"
  blocked_routes="$(jq -sc 'map(select(.exit_code == 2) | .route) | unique | sort' "$event_log")"
  detail='approved live probe completed; matrix contains only sanitized route identities and structural field names'
fi

enforced_routes='[]'
if [[ "$blocked_routes" != '[]' && ! -e "$probe_repo/forbidden.txt" && ! -e "$probe_repo/forbidden-command-marker" ]]; then
  enforced_routes="$blocked_routes"
fi
if [[ "$loaded" == true ]]; then
  degraded=false
fi
unproven_events="$(jq -cn --argjson expected "$surface_expected_events" --argjson observed "$observed_events" '$expected - $observed')"

matrix="$(jq -n \
  --arg host "$host" \
  --arg surface "$surface" \
  --arg version "$host_version" \
  --arg detail "$detail" \
  --arg action "$action_required" \
  --argjson configured "$configured" \
  --argjson discoverable "$discoverable" \
  --argjson loaded "$loaded" \
  --argjson observed "$observed_events" \
  --argjson enforced "$enforced_routes" \
  --argjson degraded "$degraded" \
  --argjson unsupported "$unsupported" \
  --argjson expected_events "$expected_events" \
  --argjson surface_expected_events "$surface_expected_events" \
  --argjson unproven_events "$unproven_events" \
  '{
    host: $host,
    surface: $surface,
    host_version: (if $version == "" then null else $version end),
    configured: $configured,
    discoverable: $discoverable,
    loaded: $loaded,
    observed: $observed,
    enforced: $enforced,
    inferred: ($host == "opencode" or $host == "kilo"),
    degraded: $degraded,
    unsupported: $unsupported,
    artifact_events: $expected_events,
    expected_events: $surface_expected_events,
    unproven_events: $unproven_events,
    detail: $detail,
    action_required: $action
  }')"

if [[ "$json_output" == true ]]; then
  printf '%s\n' "$matrix"
else
  jq -r '"\(.host)/\(.surface): configured=\(.configured), discoverable=\(.discoverable), loaded=\(.loaded), unsupported=\(.unsupported)\n\(.detail)\nNext: \(.action_required)"' <<<"$matrix"
fi
