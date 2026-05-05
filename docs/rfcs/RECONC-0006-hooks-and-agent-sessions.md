# RECONC-0006: Hooks And Agent Sessions

- Status: Frozen
- Contract: git, Claude Code, Codex, and generic-agent integration

## Hook Kinds

`reconc hook generate` and `reconc hook install` support:

| Kind | Target | Enforcement |
|---|---|---|
| `git-pre-commit` | `.git/hooks/pre-commit` | Runs `reconc ci --staged` before commit. |
| `claude-code` | `.claude/settings.json` | Session, pre-write, post-tool, stop, and cleanup hooks. |
| `codex` | `.codex/hooks.json` | Session, Bash pre/post hooks, and stop gate. |

Installers are idempotent. Reconc-owned JSON hook entries are
identified by `reconc hook runtime` command prefixes and replaced on
reinstall; unrelated user config is preserved.

## Claude Code Guarantee

Claude Code provides file-tool hooks, so `reconc` can enforce:

- pre-write blocking for `deny_write` and blocking `require_read`
- post-tool evidence tracking for reads, writes, commands, and command
  outcomes
- stop-gate blocking for unmet command, claim, coupling, evidence, and
  script requirements
- session-end cleanup while saved reports remain available

## Codex Guarantee

Codex hooks are Bash-centered in current supported wiring:

- Bash command interception for `forbid_command`
- command evidence collection
- stop-gate blocking for unmet invariants

File-read and file-write enforcement is softer than Claude Code and
depends on AGENTS.md instructions plus explicit CLI checks. Git
pre-commit remains the hard backstop.

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
