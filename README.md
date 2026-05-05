# reconc -- Repository Control Compiler

`reconc` is the Repository Control Compiler: a small Go CLI that compiles
repository policy into a deterministic control contract for AI-assisted coding
workflows.

It checks proposed writes, command evidence, hook events, claims, and git diffs
against one repo-local policy model. One binary, offline by default, no Docker,
no daemon, no runtime network dependency.

## What It Does

- compiles policy from `AGENTS.md`, `.reconc.yml`, presets, templates, and
  policy files into `.reconc/policy.lock.json`
- blocks or warns on protected writes, missing reads, missing test commands,
  missing claims, stale evidence, and unsafe hook activity
- fails closed on stale lockfiles, schema drift, invalid globs, unsupported rule
  kinds, and repository-root mismatch
- installs git hooks plus native Claude Code and Codex hooks when those agent
  configs exist
- gives agents one short remediation path with `reconc next .` and one final
  task gate with `reconc done .`

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
reconc setup .
```

`setup` scaffolds missing repo policy, compiles the local lockfile, installs a
git pre-commit hook, and wires Claude Code / Codex hooks when `.claude/` or
`.codex/` already exist.

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

Claude Code and Codex have native hook wiring. OpenCode and other agents use
the same CLI loop plus git pre-commit as the hard repository backstop.

## Policy Files

Commit:

- `.reconc.yml` for repo policy configuration
- `AGENTS.md` for agent-facing project instructions
- `skills/reconc/SKILL.md` for portable agent usage

Do not commit generated runtime state:

- `.reconc/policy.lock.json`
- `.reconc/audit.jsonl*`
- `.reconc/sessions/`
- `dist/`

## Documentation

Current product documentation lives in `docs/documentation.md`. That file is
the source of truth for setup, workflow, architecture, release, security, and
git-ignore policy.

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

`reconc` is released on the `v0.4.x` line. Core local gates pass, and release
artifacts are produced by the GitHub release workflow when a `reconc-v*` tag is
pushed.

## License

MIT License. Copyright (c) 2026 Christopher Schulze.
