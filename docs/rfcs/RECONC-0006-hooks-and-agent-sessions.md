# RECONC-0006: Hooks And Agent Sessions

- Status: Frozen
- Contract: git, Claude Code, Codex, GitHub Copilot, Cursor, OpenCode, Devin CLI, Antigravity CLI, Kilo Code, Grok Build, Kimi Code CLI, Oh My Pi CLI, Pi Coding Agent, ZCode, and generic-agent integration

## Hook Kinds

`reconc hook generate` and `reconc hook install` support:

| Kind | Target | Enforcement |
|---|---|---|
| `git-pre-commit` | active Git hooks path (`.git/hooks/pre-commit` by default) | Runs `reconc ci --staged` before commit. |
| `claude-code` | `.claude/settings.json` | Prompt, permission, tool, failure, notification, compaction, subagent, session, stop, and namespaced MCP hooks. |
| `codex` | `.codex/hooks.json` | Released prompt, permission, tool, compaction, subagent, session-start, session-end, stop, and namespaced MCP hooks. |
| `github-copilot` | `.github/hooks/reconc.json` | Version-1 repository hooks for Copilot CLI and coding agent, with contract-tested tool, lifecycle, compaction, subagent, and Stop translation. |
| `cursor` | `.cursor/hooks.json` | Desktop/CLI session and workspace liveness, native prompt/subagent decisions, write/shell policy, post-tool evidence, and Stop gate. |
| `opencode` | `.opencode/plugins/reconc.js` | Project plugin for prompt, permission, complete tool outcomes, terminal errors, compaction, session lifecycle, and idle stop gate. |
| `devin-cli` | `.devin/hooks.v1.json` | Session, tool, permission, stop, cleanup, and post-compaction hooks. |
| `antigravity` | `.agents/hooks.json` | Invocation, tool, and stop hook group under the top-level `reconc` key. |
| `kilo` | `.kilo/plugin/reconc.js` | Thin project plugin for prompt, permission, complete tool outcomes, terminal errors, compaction, session lifecycle, and idle handling. |
| `grok` | `.grok/hooks/reconc.json` | Native lifecycle, strict PreToolUse, capability-probed no-leader Stop, compaction, permission-denial, and subagent events. |
| `kimi-code` | `$KIMI_CODE_HOME/config.toml` | Explicit user-global integration for 16 of the host's twenty events; commands discover an initialized Reconc repository before acting. |
| `omp` | `.omp/extensions/reconc.ts` | Project ExtensionAPI adapter for native session, input, approval, tool, user-shell, user-Python, compaction, shutdown, and awaited `session_stop` events. |
| `pi` | `.pi/extensions/reconc.ts` | Trust-aware project extension for native session, input, blocking tool and user-shell calls, outcomes, compaction, shutdown, and inferred settled continuation. |
| `zcode` | `.zcode/config.json` | Project hook integration for all seven native lifecycle, prompt, tool, permission, outcome, and synchronous Stop events. |

Installers are idempotent. Reconc-owned JSON hook entries are
identified by `reconc hook runtime` command tokens and replaced on
reinstall; unrelated user config is preserved. The OpenCode installer
updates only the reconc-managed project plugin and refuses to overwrite
non-reconc plugin content without `--force`. Kilo Code and Grok managed files
use the same refusal rule. GitHub Copilot owns only
`.github/hooks/reconc.json` and never overwrites foreign content at that path,
even with `--force`; sibling repository hook files are preserved. Grok owns
only `reconc.json` and preserves every other file under `.grok/hooks/`.
OMP owns only `.omp/extensions/reconc.ts`, preserves every sibling extension,
and never overwrites foreign content at its dedicated path, including with
`--force`.
Pi owns only `.pi/extensions/reconc.ts`, applies the same foreign-content
refusal, and never edits Pi's project trust or settings files.
ZCode owns only exact Reconc process entries under `hooks.events` and the
required `hooks.enabled` activation in `.zcode/config.json`. It preserves
foreign settings, events, and commands. Invalid nested shapes fail unless
explicit force first publishes the exact prior file as a private,
content-addressed backup.
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

Kimi Code is the only user-global hook target and is never selected by
repository init, bootstrap, `--hook all`, or scaffold sync. Its explicit
install takes no repository argument, validates existing TOML, merges one
marker-owned block under a cross-process lock, preserves unrelated bytes and
mode, and refuses drift without `--force`. Forced replacement first preserves
the exact prior config in a private content-addressed backup. Uninstall removes
only a generator-exact managed block.

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
`.kilo/plugin/reconc.js`, `.grok/hooks/reconc.json`,
`.omp/extensions/reconc.ts`, `.pi/extensions/reconc.ts`, and
`.zcode/config.json`. Template scaffolds are never synced from
a source-specific harness.

## ZCode Guarantee

ZCode discovers repository configuration at `.zcode/config.json` and snapshots
it when a session starts. Reconc installs `SessionStart`, `UserPromptSubmit`,
`PreToolUse`, `PermissionRequest`, `PostToolUse`, `PostToolUseFailure`, and
`Stop` as direct process executors with argument arrays and native millisecond
timeouts. Install and uninstall therefore require a new ZCode session before
the host can observe the change.

The adapter strictly normalizes the documented snake_case envelope, canonical
repository `cwd`, session identity, available tool-call identity, tool input,
result, error, interrupt, and Stop fields. Hard `PreToolUse` blocks use exit
code 2, `PermissionRequest` denials use the native decision object, and Stop
continuation uses `decision: "block"`; malformed fail-closed requests use the
native exit-code-2 shortcut. Observation routes and host timeouts fail open.
Native Stop blocking is bounded by ZCode to three consecutive blocks. The fixture pins ZCode 3.3.6
hook documentation revision
`sha256:9c5043d1b06816fa3435b261a78ba32ac8bf08b6a098ded6262ce5ed0adf4f9b`.

## Kimi Code CLI Guarantee

Kimi Code hooks are global, so each generated command invokes bare `reconc`
and relies on the host working directory. The internal entrypoint silently
no-ops unless normal discovery finds an explicit Reconc configuration, then
binds the native payload `cwd` to that repository before shared policy or
session handling. Reconc installs all 16 documented native events and strictly
normalizes `hook_event_name`, `session_id`, `cwd`, tool name/input/call ID,
tool output, error, reason, and Stop state.

Kimi blocks only when `PreToolUse`, `UserPromptSubmit`, or `Stop` exits 2.
Exit zero allows; every other non-zero exit, crash, and timeout is host
fail-open. Reconc therefore converts an intentional shared-runtime denial or
Stop block into exact exit code 2 with a bounded stderr reason. Post-tool output
contains no authoritative exit status, so it never becomes positive command
success by inference. Static TOML identity and isolated adapter tests are not
live host proof; exact route liveness remains separate in `hook status`.

## Oh My Pi CLI Guarantee

OMP discovers project extensions under `.omp/extensions/` from its working
directory. The generated TypeScript module registers only documented
`ExtensionAPI` handlers and delegates policy, evidence, session state, and
continuation decisions to Reconc's Go runtime through the shared repo-local
wrapper.

`tool_call` is the blocking pre-action boundary, and `user_bash` is the same
boundary for a shell command the user types: OMP publishes it with the same
full-replacement result contract, so both reach one decision. `user_python` is
observed and never decided, because the policy vocabulary reads shell grammar
and Python source is not a command line. That code can start a shell, so the
execution, its working directory, and the size of the code are recorded while
the code itself never leaves the host, and the user-shell guarantee is stated
as covering shell commands only. OMP's
`tool_approval_requested` and `tool_approval_resolved` events are observation
events and are never misrepresented as a permission-decision surface.
`tool_result` always routes success or failure from the authoritative
`isError` field. Successful built-in Bash results synthesize exit code zero;
failed Bash results never receive a fabricated exit status. Output text is
never interpreted as process status.

Native awaited `session_stop` maps Reconc block or continuation output to
OMP's `decision`/`reason` or `continue`/`additionalContext` result. OMP invokes
this event for the main agent only and caps continuation at eight turns. An
aborted host signal wins immediately. Session start, input, approval, result,
automatic compaction, and shutdown are fail-open observations; pre-tool and
Stop runtime, timeout, malformed, invalid UTF-8, or oversized-output failures
fail closed. The Stop route uses a 29-second internal budget inside OMP's
30-second extension-handler deadline; the generated shutdown route uses a
one-second Reconc budget inside OMP's two-second handler budget.

## Pi Coding Agent Guarantee

Pi discovers `.pi/extensions/*.ts` and nested `index.ts` extensions only after
project trust. Reconc owns the exact `.pi/extensions/reconc.ts` file and
evaluates saved canonical-path trust using Pi's nearest-parent rule plus
`defaultProjectTrust`. Status does not treat interactive approval or one-run
`pi --approve` as persisted configuration, and Reconc never mutates the trust
store. The contract fixture pins official source revision
`ac4ac9eaf69f2b01ca3af984a5c48f3b99b84278`, package
`@earendil-works/pi-coding-agent` v0.84.1. The companion OMP fixture remains at
revision `06343fef4200c4e32d18f08df5a6a8bd84dcc710`, v17.2.4.

The generated typed extension registers `session_start`, `input`, `tool_call`,
`tool_result`, `user_bash`, `session_before_compact`, `session_compact`,
`agent_settled`, and `session_shutdown`. Awaited `tool_call` blocks through the
native `{block, reason}` result; the host's optional `terminate` hint stays
unused because no Reconc policy mode ends a session. `user_bash` denial returns a complete synthetic
shell result with exit code 2, while allow returns no replacement result and
preserves host execution. Both fail closed on malformed or failed Reconc
decisions. Tool results are observational and route exact `isError`; only a
successful built-in `Bash` result synthesizes exit code zero. Failed output is
never parsed as process status, and Pi exposes no post-`user_bash` result.

Pi has no native permission event, MCP discriminator, synchronous Stop event,
or delivery acknowledgement from `sendUserMessage`. Permission and exact MCP
identity therefore use only the generic pre-tool boundary; strict unclassified
MCP denial is unavailable. `agent_settled` provides bounded fail-open inferred
continuation with 1,024 session entries, one in-flight request per generation,
and ten requested continuations per session. Reconc-generated input is
consumed without advancing activity. Requested delivery is not acceptance.
Session, input, result, compaction, settled continuation, and shutdown failures
remain observational, while the host abort signal releases immediately.

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
invariants. Codex has no separate failed-tool route; its `PostToolUse`
payload carries the outcome, and Reconc classifies failures from it. Bootstrap writes
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
from an explicit successful host result. Claude Code and Codex reach the same
enforcement through the `mcp__<server>__<tool>` namespace group Reconc installs
on their generic tool events: the namespace is the discriminator, so a route
that fires is an MCP call by construction. OpenCode, Kilo, OMP, Pi, and ZCode generic hooks
enforce exact configured tool identities, but cannot distinguish an
unconfigured MCP tool from a built-in or custom tool. Strict unclassified deny
is therefore unavailable on those five surfaces and is reported as a
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
session-end reasons release. The generated Stop budget is 600 seconds.
Distributions without that native contract may use the optional local leader
fallback.

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
