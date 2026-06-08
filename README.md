# reconc -- Repository Control Compiler

`reconc` is the Repository Control Compiler: a small Go CLI that turns
repository policy into a deterministic control plane for AI-assisted coding
workflows.

It checks proposed writes, command evidence, hook events, claims, and git diffs
against one repo-local policy model. One binary, offline by default, no Docker,
no daemon, no runtime network dependency.

It does not pretend that an LLM is deterministic. It makes the boundaries around
agent work deterministic: what can be touched, what evidence is required, which
runtime events are allowed to continue, and why a gate passed, warned, or
blocked.

## What It Does

- compiles policy from `AGENTS.md`, `.reconc.yml`, presets, templates, and
  policy files into `.reconc/policy.lock.json`
- blocks or warns on protected writes, missing reads, missing test commands,
  missing claims, stale evidence, and unsafe hook activity
- fails closed on stale lockfiles, schema drift, invalid globs, unsupported rule
  kinds, and repository-root mismatch
- installs git hooks plus native Claude Code, Codex, Cursor, OpenCode, and
  Antigravity hooks when those agent configs exist
- controls autonomous agent continuation with prompt-scoped `/runloop` state,
  stop files, no-progress guards, and append-only decision logs
- gives agents one short remediation path with `reconc next .` and one final
  task gate with `reconc done .`

## Why It Matters

Coding agents can write files, run shell commands, continue after stop hooks, and
drift away from the workflow you thought they were following. Prompt instructions
alone are not a control layer.

`reconc` gives teams policy-as-code for agentic development: fail-closed hooks,
deterministic lockfiles, evidence-based checks, scoped autonomous continuation,
and audit-friendly decisions that can be reviewed by humans, CI, or another
agent.

## Install

Build from source in this repository:

```bash
go build -o reconc ./cmd/reconc
./reconc --help
```

After installing or placing the binary on `PATH`, use `reconc` directly.

## Use In A Repository

Bootstrap policy and hooks:

```bash
reconc bootstrap .
```

`bootstrap` scaffolds missing repo policy, compiles the local lockfile, installs
a git pre-commit hook, and wires Claude Code / Codex / Cursor / OpenCode /
Antigravity hooks when `.claude/`, `.codex/`, `.cursor/`, `.opencode/` or
`.agents/` already exist.

For the full repo-local governance rollout with the harness, `start.md`, TASK
files, root scaffold, and repo-local release binaries, have an agent follow
`harness/template/BOOTSTRAP.md` from the copied Reconc toolkit. The CLI
`bootstrap` command is intentionally the minimal policy/hook bootstrap, not the
full workflow-package installer.

Daily loop:

```bash
reconc status .
reconc check . --write path/to/file
reconc next .
reconc done .
```

For staged changes, use the git-aware check:

```bash
reconc ci . --staged \
  --read docs/documentation.md \
  --command-success 'go test ./...'
```

## Minimal Example Policy

Copy this into `.reconc.yml` in a Go repository:

```yaml
default_mode: warn
extends:
  - default

rules:
  - id: go-tests-before-source-done
    kind: require_command_success
    mode: block
    when_paths:
      - "cmd/**/*.go"
      - "internal/**/*.go"
      - "go.mod"
      - "go.sum"
    commands:
      - "go test ./..."
    message: Go source or dependency changes require a successful full test run.
```

Then run:

```bash
reconc compile .
reconc check . --write internal/example.go
reconc check . --write internal/example.go --command-success 'go test ./...'
```

The second command shows the missing evidence. The third command supplies the
required test result and should pass unless another rule blocks it.

Exit codes are stable for humans, agents, and CI:

- `0`: pass, warn, or informational success
- `1`: runtime or input error
- `2`: blocking policy violation

## Agent Skill

The repo ships an agent-facing skill at `skills/reconc/SKILL.md`.

Use it as the reconc operating guide for Codex, OpenCode, Claude Code, and
other coding agents. The skill gives every agent the same workflow:

- check policy health before work
- collect truthful read, write, command, and claim evidence
- remediate blocks with `reconc next .`
- run `reconc done .` before claiming completion
- distinguish native hook enforcement from CLI self-checks

Claude Code, Codex, Cursor, OpenCode and Antigravity have repo-local
prompt/tool/stop hook wiring;
Codex also needs `hooks = true` in an active `config.toml` and routes
`apply_patch` through Reconc by parsing patch headers from
`tool_input.command`. Cursor Desktop uses `.cursor/hooks.json` with
`preToolUse` as the pre-write gate, `afterFileEdit`/`afterTabFileEdit` plus
`postToolUse` as evidence backstops for Cursor write aliases including
`StrReplace`, `Delete`, and `FileEdit`, `beforeSubmitPrompt` for standalone
`/runloop`, and `stop` via Cursor-native `followup_message`. Clean Cursor
hook paths emit explicit `{"continue":true,"permission":"allow"}` JSON because
Cursor fail-closed hooks treat empty stdout as hook failure. OpenCode uses
`.opencode/plugins/reconc.js`
with `chat.message`, `tool.execute.*`, `permission.ask`, and `session.idle`;
Antigravity uses `.agents/hooks.json` with `PreInvocation`, `PreToolUse`,
`PostToolUse`, `PostInvocation`, and `Stop`; Reconc stores Antigravity
PreTool metadata as pending evidence so PostToolUse can record exact
read/write/command evidence even when the post payload only carries the step
index/result.
Runloop activation is prompt-only and requires a standalone `/runloop`
slash-command flag in sanitized real user prompt text, so quoted transcripts,
hook prompts, stop feedback, code fences, tool text, and errors cannot start
it accidentally. Runloop runs are session- and runtime-scoped: a normal
same-session prompt stops that run, except a same-session `/btw` side-channel
prompt, which preserves the active run; prompts, interrupts, session ends, or
stop markers from another agent runtime or session in the same repo must not
stop the active run.
`.reconc/runloop/stop` is scoped to the active run and agent runtime and
clears when a new standalone `/runloop` prompt starts a run. Stop hooks cache
only the policy report, never the final stop output; `awaiting_continuation`
alone is not a hard stop reason, so Reconc may re-emit the continuation prompt
until progress or the no-progress guard decides. Runloop decisions are logged
to `.reconc/runloop/decisions.jsonl`. Repeated identical policy blocks stay
blocking but shrink to rule IDs plus the saved report path. All platforms
still use git pre-commit as the hard repository backstop. PreToolUse evaluates
only pre-execution write rules; all PostToolUse / after-shell events record
evidence only and never run repo-wide policy audits. Stop and explicit Reconc
checks remain the hard enforcement points. The Stop fingerprint uses git status
with default `--untracked-files=normal`, dirty-path content/index hashes, and a
per-session report lock instead of full `git diff --binary` output or repeated
parallel checks. Set `RECONC_STOP_FINGERPRINT_UNTRACKED=all` only when a repo
needs every untracked path in the Stop cache key.
When multiple active `require_script` rules call the same `run-workflow-audit`
runner, Reconc batches them through `--batch-json` in one process while still
mapping pass/block output back to the original rule IDs.

## Policy Files

Commit:

- `.reconc.yml` for repo policy configuration
- `AGENTS.md` for agent-facing project instructions
- `skills/reconc/SKILL.md` for portable agent usage

Do not commit generated runtime state:

- `.reconc/policy.lock.json`
- `.reconc/audit.jsonl*`
- `.reconc/cache/`
- `.reconc/locks/`
- `.reconc/sessions/`
- `.reconc/reports/`
- `dist/`

## Documentation

Current product documentation lives in `docs/documentation.md`. That file is
the source of truth for installation, workflow, architecture, release, security,
and git-ignore policy.

- `docs/architecture.md` covers contributor internals and the hook-runtime
  threat model.
- `docs/commands.md` is the full command reference; `reconc <command> --help`
  remains the exact flag reference.
- `docs/rfcs/` contains frozen contracts for the lockfile, reports, rule
  kinds, presets, templates, and hooks.
- local planning files such as `docs/todo.md`, `docs/todo/`, and
  `CHANGELOG.md` are ignored and are not part of the published repo state.

Security policy lives in `SECURITY.md`.

For command details:

```
reconc <command> --help
```

## Status

`reconc` is released on the `v0.5.x` line. Core local gates pass, and release
artifacts are produced by the GitHub release workflow when a `reconc-v*` tag is
pushed.

## License

MIT License. Copyright (c) 2026 Christopher Schulze.
