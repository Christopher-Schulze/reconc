# RECONC-0006: Hooks And Agent Sessions

- Status: Frozen
- Contract: git, Claude Code, Codex, Cursor, OpenCode, Devin CLI, Antigravity CLI, Kilo Code, Grok Build, and generic-agent integration

## Hook Kinds

`reconc hook generate` and `reconc hook install` support:

| Kind | Target | Enforcement |
|---|---|---|
| `git-pre-commit` | active Git hooks path (`.git/hooks/pre-commit` by default) | Runs `reconc ci --staged` before commit. |
| `claude-code` | `.claude/settings.json` | Prompt, permission, tool, failure, compaction, subagent, session, and stop hooks. |
| `codex` | `.codex/hooks.json` | Released prompt, permission, tool, compaction, subagent, session-start, and stop hooks. |
| `cursor` | `.cursor/hooks.json` | Session, native `beforeSubmitPrompt`, write/shell policy, post-tool evidence, and stop gate. |
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
use the same refusal rule; Grok owns only `reconc.json` and preserves every
other file under `.grok/hooks/`.
The Git installer resolves the same active `core.hooksPath` used by status,
updates managed content idempotently, supports linked-worktree common Git
storage, and refuses to write into a shared external hooks directory.
Every non-Git installer target is resolved through existing parent symlinks and
must remain inside the selected repository. Scaffold sync preflights all target
paths before its first write. Forced malformed-config backups are
content-addressed, create-only, `0600`, file-synced, and parent-directory-synced
before the managed artifact is published.

The typed registry owns event coverage, native names, fallbacks, failure and
timeout policy, timeout/output budgets, paths, install strategies, and
activation probes. `reconc hook status` reports configuration truth without
claiming that a live agent process already loaded the artifact.

`reconc hook sync-scaffold <repo-root-scaffold>` writes the
source-controlled scaffold twins from the same generator:
`.githooks/pre-commit`, `.codex/hooks.json`, `.cursor/hooks.json`,
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

## OpenCode And Kilo Code Guarantee

Both thin Bun adapters preserve complete `tool.execute.after`
title/output/metadata, block pre-tool and permission runtime failures, route
user prompts plus pre/post-compaction and session lifecycle, and convert
terminal `message.part.updated` tool errors into deduplicated failure evidence.
Policy, session state, and recovery context remain in the Go runtime. Their
`session.idle` continuation boundary is best-effort and fail-open rather than a
synchronous native Stop gate; git pre-commit remains the hard repository
backstop.

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
cleanup for later inspection.

## Payload Safety

Hook runtime payloads are untrusted:

- stdin payload size is capped at 64 MiB so multi-megabyte edit bodies work without permitting unbounded input
- JSON depth is capped before unmarshal
- malformed pre-write and stop payloads fail closed
- post-tool observation payload failures fail open with warnings
- payload command strings are matched as data and are never executed

Only policy-authored `require_script` entries can execute subprocesses,
and those scripts must be repo-local.
