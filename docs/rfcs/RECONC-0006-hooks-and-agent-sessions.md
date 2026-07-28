# RECONC-0006: Hooks And Agent Sessions

- Status: Frozen
- Contract: git, Claude Code, Codex, GitHub Copilot, Cursor, OpenCode, Devin CLI, Antigravity CLI, Kilo Code, Grok Build, and generic-agent integration

## Hook Kinds

`reconc hook generate` and `reconc hook install` support:

| Kind | Target | Enforcement |
|---|---|---|
| `git-pre-commit` | active Git hooks path (`.git/hooks/pre-commit` by default) | Runs `reconc ci --staged` before commit. |
| `claude-code` | `.claude/settings.json` | Prompt, permission, tool, failure, compaction, subagent, session, and stop hooks. |
| `codex` | `.codex/hooks.json` | Released prompt, permission, tool, compaction, subagent, session-start, and stop hooks. |
| `github-copilot` | `.github/hooks/reconc.json` | Version-1 repository hooks for Copilot CLI and coding agent, with contract-tested tool, lifecycle, compaction, subagent, and Stop translation. |
| `cursor` | `.cursor/hooks.json` | Desktop/CLI session and workspace liveness, native prompt/subagent decisions, write/shell policy, post-tool evidence, and Stop gate. |
| `opencode` | `.opencode/plugins/reconc.js` | Project plugin for prompt, permission, complete tool outcomes, terminal errors, compaction, session lifecycle, and idle stop gate. |
| `devin-cli` | `.devin/hooks.v1.json` | Session, tool, permission, stop, cleanup, and post-compaction hooks. |
| `antigravity` | `.agents/hooks.json` | Invocation, tool, and stop hook group under the top-level `reconc` key. |
| `kilo` | `.kilo/plugin/reconc.js` | Thin project plugin for prompt, permission, complete tool outcomes, terminal errors, compaction, session lifecycle, and idle handling. |
| `grok` | `.grok/hooks/reconc.json` | Native lifecycle, strict PreToolUse, capability-probed no-leader Stop, compaction, permission-denial, and subagent events. |

Installers are idempotent. Reconc-owned JSON hook entries are
identified by `reconc hook runtime` command tokens and replaced on
reinstall; unrelated user config is preserved. The OpenCode installer
updates only the reconc-managed project plugin and refuses to overwrite
non-reconc plugin content without `--force`. Kilo Code and Grok managed files
use the same refusal rule. GitHub Copilot owns only
`.github/hooks/reconc.json` and never overwrites foreign content at that path,
even with `--force`; sibling repository hook files are preserved. Grok owns
only `reconc.json` and preserves every other file under `.grok/hooks/`.
The Git installer resolves the same active `core.hooksPath` used by status,
updates managed content idempotently, supports linked-worktree common Git
storage, and refuses to write into a shared external hooks directory.
Every non-Git installer target is resolved through the operating system's
filesystem identity and must remain inside the selected repository. Unix
symlinks and Windows reparse points, including junctions, are followed;
Windows 8.3 aliases are normalized. Scaffold sync preflights all target paths
before its first write. Forced malformed-config backups are
content-addressed, create-only, `0600`, file-synced, and parent-directory-synced
before the managed artifact is published.

The typed registry owns event coverage, native bindings, fallbacks, response
modes, failure and timeout policy, timeout/output budgets, paths, install
strategies, documented surfaces, and activation probes. `reconc hook status`
reports static configuration separately from per-route liveness and never
turns a configured artifact into a live or enforced claim.

`reconc hook sync-scaffold <repo-root-scaffold>` writes the
source-controlled scaffold twins from the same generator:
`.githooks/pre-commit`, `.codex/hooks.json`, `.github/hooks/reconc.json`, `.cursor/hooks.json`,
`.agents/hooks.json`, `.claude/settings.json`, and
`.opencode/plugins/reconc.js`, `.devin/hooks.v1.json`,
`.kilo/plugin/reconc.js`, and `.grok/hooks/reconc.json`. Template scaffolds are never synced from
a source-specific harness.

## Claude Code Guarantee

Claude Code provides file-tool hooks, so `reconc` can enforce:

- pre-write blocking for `deny_write` and blocking `require_read`
- post-tool evidence tracking for reads, writes, commands, and command
  outcomes
- stop-gate blocking for unmet command, claim, coupling, evidence, and
  script requirements
- bounded context recovery through `SessionStart` with matcher `compact`
- native prompt, permission-denied, failed-tool, Stop-failure, subagent, and
  pre/post-compaction observation with explicit route timeouts
- session-end cleanup while saved reports remain available

## Codex Guarantee

Codex exposes prompt, session-start, tool, permission, pre/post-compaction,
subagent, evidence, and Stop routes. Reconc extracts write paths from native
tool fields and `apply_patch` headers, intercepts shell commands, infers failed
Bash outcomes from the single `PostToolUse` contract, and blocks Stop on unmet
invariants. Codex has no `SessionEnd` or separate failed-tool route. Bootstrap writes
`hooks = true` under `[features]`; a root-level lookalike does not activate the
host. Git pre-commit remains the hard repository backstop.

## GitHub Copilot Guarantee

GitHub Copilot CLI and coding agent discover version-1 repository hooks under
`.github/hooks/*.json`. Reconc generates one managed `reconc.json` with
documented PascalCase compatibility events and native `subagentStart`. The
coding agent uses the `bash` command in its Linux `/workspace` checkout;
Copilot CLI can also use the generated PowerShell route. Reconc validates the
payload `cwd` against the selected repository, normalizes documented
snake_case payloads and `tool_result`, and translates control results into the
exact Copilot schemas for PreToolUse, PermissionRequest, PostToolUseFailure,
Stop, and SubagentStop.

`PermissionRequest` and `Notification` are CLI-only. Cloud permission control
therefore uses `PreToolUse`. Command failures are converted into explicit
denial or Stop blocking while Reconc can still respond, but GitHub documents
all hook timeouts as fail-open. The host also bounds repeated Stop blocks. The
adapter exposes no `PostCompact` route because the host contract has none.
Generated and configured status proves static contract integrity only;
per-route status liveness is required before claiming actual host execution.

## Cursor Guarantee

Cursor uses one registry-generated `.cursor/hooks.json`. The registry
classifies all 21 current events exactly once and installs only the 17 events
with repository-policy semantics: session start/end, prompt, pre/post/failure
tool use, shell pre/passive-post, dedicated MCP pre/post, file-edit and Tab
write evidence, subagent start/stop, pre-compaction, Stop, and sessionless
workspace liveness. Reconc
intentionally excludes read-prevention, assistant response/thought capture,
and Tab pre-read. `workspaceOpen` is validated and privacy-redacted, records
only route liveness, never creates session/repository evidence, and returns no
plugin paths.

`postToolUse` is the sole authoritative generic success signal.
`postToolUseFailure` records failure and no positive evidence.
`afterShellExecution` records passive liveness because its current host payload
contains output and duration but no authoritative exit status.
`afterFileEdit` and `afterTabFileEdit` are successful write fallbacks
deduplicated against generic delivery. Cursor tool and subagent decisions
return exact permission objects, prompt submission returns the native
`continue` object, observation/workspace routes return `{}`, and Stop plus
subagent Stop use bounded `followup_message`.

The same project file can be discovered by desktop Agent, Cmd+K, Tab, Cursor
CLI, and eligible cloud-agent execution, but shared configuration is not
event-delivery parity. Tab owns only its write route. Cloud agents do not
provide session start/end, dedicated MCP, or Tab routes. CLI interactive and
print mode use `agent` (`cursor-agent` remains an alias). Their documented
registry set is session start/end, prompt, generic pre/post tool, Stop, and
workspace liveness. Reconc uses identical normalization and enforcement
whenever the host emits the same event, but never simulates a missing
pre-action boundary from output streams. Cursor's current `AskQuestion` host
bug emits no generic tool hooks in IDE or CLI, so that action is outside
strict Reconc enforcement.

## OpenCode And Kilo Code Guarantee

Both thin Bun adapters preserve complete `tool.execute.after`
title/output/metadata, block pre-tool and permission runtime failures, route
user prompts plus pre/post-compaction and session lifecycle, and convert
terminal `message.part.updated` tool errors into deduplicated failure evidence.
Shell success requires the exact integer `output.metadata.exit`; non-zero,
missing, malformed, conflicting, timed-out, aborted, or explicit-error
outcomes record failure and never positive command evidence. Output text is
never interpreted as process status.

The generated runner drains stdout and stderr concurrently, enforces one
combined 8 KiB budget, rejects invalid UTF-8 and truncated decision JSON, and
kills and awaits timed-out subprocesses. Policy, session state, and recovery context
remain in the Go runtime.

Their `session.idle` continuation boundary is a bounded best-effort,
fail-open state machine rather than a synchronous native Stop gate. Per-session
activity generations, one in-flight request, 1,024-session capacity, and a
ten-accepted-continuation cap prevent duplicate or unbounded submission. The
adapter calls only the current flat SDK request
`client.session.promptAsync({sessionID, messageID, parts})`, correlates the
injected `chat.message` by that exact caller-owned identifier, waits for request
acceptance, and never falls back to synchronous `prompt`. Git pre-commit
remains the hard repository backstop.

## MCP Side-Effect Guarantee

The compiler accepts an optional typed `mcp` contract in `.reconc.yml`.
Mappings classify one exact `(platform, server_fingerprint, tool)` identity as
`repository_read`, `repository_write`, `command`, or `external`. Repository
and command effects select values only through configured RFC 6901 JSON
Pointers. Fingerprint presence is identity: no qualified/unqualified fallback
exists. Wrong identity, malformed selected value, repository escape, or
outcome uncertainty produces no positive evidence.

Cursor's dedicated `beforeMCPExecution` can enforce exact mappings and
`unclassified: deny`; `afterMCPExecution` accepts repository evidence only
from an explicit successful host result. OpenCode and Kilo generic hooks
enforce exact configured tool identities, but cannot distinguish an
unconfigured MCP tool from a built-in or custom tool. Strict unclassified deny
is therefore unavailable on those two surfaces and is reported as a
limitation, not an enforcement success.

MCP audit and status retain only redacted platform/tool/fingerprint/effect
identity and bounded counters. Server locators, credentials, arguments,
results, prompts, and command bodies are not persisted.

## Grok Build Guarantee

Grok's native hook envelope is camelCase and uses Grok tool names. Reconc
normalizes those fields and maps `run_terminal_command`, `run_terminal_cmd`, `write`,
`search_replace`, `hashline_edit`, `read_file`, `hashline_read`, `grep`,
`hashline_grep`, and `list_dir` into the canonical policy/evidence contract.
PreToolUse emits Grok's exact allow/deny JSON. The Grok host is fail-open on
hook errors and timeouts, so the generated route plus wrapper convert ordinary
Reconc execution failures into an explicit deny. Grok's own hard process
timeout remains fail-open.

Project hooks require Grok folder trust. `reconc doctor --deep` uses
`grok inspect --json` to verify trust, project-owned source metadata, the
generator-exact managed hook and executable wrapper, and all 14 exact native
route command tokens; prefix collisions do not count. The guarded PreToolUse
path accepts only one exact allow/deny JSON object and converts runtime errors,
timeouts, empty or multiline output, and malformed decisions into explicit
deny JSON. Reconc also emits exact native Stop block JSON without a leader and
marks eligible live Stops strict across `stopHookActive` re-entry. It treats
that output as synchronously enforced only when the hook guide shipped with the
installed Grok distribution explicitly advertises blocking Stop decision
control; version strings are not accepted as capability proof. Interrupts and
session-end reasons release. The generated Stop budget is 600 seconds. `reconc
grok` remains the explicit strict ACP path.

For Grok distributions with a passive Stop contract, optional leader mode
additionally steers the TUI over the Unix socket or Windows named pipe.
Protocol-1 `_x.ai/interject` requires a
matching `GROK_SESSION_ID`; only delivered interjections count toward the
32-attempt no-progress cap. Material progress, a new block, or a clean Stop
resets the series. Capability-proven native hosts suppress duplicate
interjection.
Strict Grok Stop payloads do not use the repository-run six-event release;
their applicable bound is this 32-delivered-interjection cap.
Multiple endpoints receive fair shares of the transport budget and framed
writes complete short writes. `RECONC_GROK_STEER=0` disables only leader
steering. Deep doctor reports native Stop support separately and requires a
recognized `_x.ai/interject` response for leader compatibility.

## Generic Agents

Agents without hooks should use the CLI loop:

1. `reconc status .`
2. `reconc check . --write ... --read ... --command-success ...`
3. `reconc next .` on failure
4. `reconc done .` before final completion

## Session State

Agent session state is stored under `$RECONC_HOME` and keyed by
repository/project plus session id. It records deduped reads, writes,
commands, command results, and claims. Saved reports survive session
cleanup for later inspection. Repository identity follows symlinks and Windows
reparse points, and Claude memory matching accepts only operating-system-
confirmed component-wise mixtures of 8.3 and long-path aliases for the current
repository.

## Payload Safety

Hook runtime payloads are untrusted:

- stdin payload size is capped at 64 MiB so multi-megabyte edit bodies work without permitting unbounded input
- JSON depth is capped before unmarshal
- malformed pre-write and stop payloads fail closed
- post-tool observation payload failures fail open with warnings
- payload command strings are matched as data and are never executed

Only policy-authored `require_script` entries can execute subprocesses,
and those scripts must be repo-local.
