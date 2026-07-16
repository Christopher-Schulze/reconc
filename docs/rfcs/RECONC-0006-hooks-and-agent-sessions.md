# RECONC-0006: Hooks And Agent Sessions

- Status: Frozen
- Contract: git, Claude Code, Codex, Cursor, OpenCode, Devin CLI, Antigravity CLI, Kilo Code, Grok Build, and generic-agent integration

## Hook Kinds

`reconc hook generate` and `reconc hook install` support:

| Kind | Target | Enforcement |
|---|---|---|
| `git-pre-commit` | `.git/hooks/pre-commit` | Runs `reconc ci --staged` before commit. |
| `claude-code` | `.claude/settings.json` | Session, pre-write, post-tool, stop, and cleanup hooks. |
| `codex` | `.codex/hooks.json` | Session, Bash pre/post hooks, and stop gate. |
| `cursor` | `.cursor/hooks.json` | Session, prompt, write/shell policy, post-tool evidence, and stop gate. |
| `opencode` | `.opencode/plugins/reconc.js` | Project plugin for session start, tool before/after, and idle stop gate. |
| `devin-cli` | `.devin/hooks.v1.json` | Session, tool, permission, stop, cleanup, and post-compaction hooks. |
| `antigravity` | `.agents/hooks.json` | Invocation, tool, and stop hook group under the top-level `reconc` key. |
| `kilo` | `.kilo/plugin/reconc.js` | Thin project plugin for session, tool, permission, compaction, and idle handling. |
| `grok` | `.grok/hooks/reconc.json` | Native lifecycle, strict PreToolUse decisions, passive Stop reporting, compaction, permission-denial, and subagent events. |

Installers are idempotent. Reconc-owned JSON hook entries are
identified by `reconc hook runtime` command tokens and replaced on
reinstall; unrelated user config is preserved. The OpenCode installer
updates only the reconc-managed project plugin and refuses to overwrite
non-reconc plugin content without `--force`. Kilo Code and Grok managed files
use the same refusal rule; Grok owns only `reconc.json` and preserves every
other file under `.grok/hooks/`.

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
- session-end cleanup while saved reports remain available

## Codex Guarantee

Codex exposes session, tool, permission, evidence, and Stop routes. Reconc
extracts write paths from native tool fields and `apply_patch` headers,
intercepts shell commands, records successful and failed evidence, and blocks
Stop on unmet invariants. Codex has no `SessionEnd` route. Bootstrap writes
`hooks = true` under `[features]`; a root-level lookalike does not activate the
host. Git pre-commit remains the hard repository backstop.

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
`grok inspect --json` to verify trust and all generated native routes. Native
Stop is passive and cannot force another TUI turn. `reconc grok` provides the
strict path by driving the unmodified official ACP stdio runtime and
re-prompting the same session while Reconc returns a continuation reason.
When Grok runs in leader mode, the `grok-stop` route additionally steers the
TUI itself: it registers on the leader socket and queues the continuation
reason via `x.ai/interject`, which Grok turns into an immediate prompt turn on
an idle session. Steering requires a matching `GROK_SESSION_ID`, skips user
interrupts, is capped at 32 attempts per session, honours
`RECONC_GROK_STEER=0`, and fails open to the passive report.

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

- stdin payload size is capped
- JSON depth is capped before unmarshal
- malformed pre-write and stop payloads fail closed
- post-tool observation payload failures fail open with warnings
- payload command strings are matched as data and are never executed

Only policy-authored `require_script` entries can execute subprocesses,
and those scripts must be repo-local.
