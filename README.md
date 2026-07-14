![RECONC Repository Control Compiler](assets/reconc-hero.png)

# Repository Control Compiler

Deterministic policy gates for AI-assisted software engineering.

[![CI](https://github.com/Christopher-Schulze/reconc/actions/workflows/reconc-ci.yml/badge.svg)](https://github.com/Christopher-Schulze/reconc/actions/workflows/reconc-ci.yml)
[![Release](https://github.com/Christopher-Schulze/reconc/actions/workflows/reconc-release.yml/badge.svg)](https://github.com/Christopher-Schulze/reconc/actions/workflows/reconc-release.yml)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)
[![Offline](https://img.shields.io/badge/runtime-offline_by_default-111827)](#what-it-does)

`reconc` is a small offline Go CLI that turns repository policy into a compiled
control plane. It lets a repo decide what an AI coding agent may read, write,
claim, continue, or finish based on real evidence instead of prompt trust.

One binary. Repo-local policy. Deterministic lockfile. Native hooks for modern
agent runtimes. No daemon, no Docker requirement, no runtime network dependency.

```text
AGENTS.md + .reconc.yml + presets + templates
                  |
                  v
        reconc refresh -> .reconc/policy.lock.json
                  |
                  v
     CLI checks + git hooks + agent runtime hooks
                  |
                  v
        pass / warn / block + exact next action
```

`reconc` does not make LLMs deterministic. It makes the boundary around their
work deterministic: which files are protected, which commands must have run,
which claims must be supplied, which hook events are allowed to continue, and
why a task is allowed to be called done.

## What It Does

- compiles policy from `AGENTS.md`, `.reconc.yml`, presets, templates, and
  policy files into `.reconc/policy.lock.json`
- blocks or warns on protected writes, missing reads, missing test commands,
  missing claims, stale evidence, and unsafe hook activity
- evaluates bounded native layout, language, dependency, network/process,
  substantive-proof, and live-verification gates without extra subprocesses
- fails closed on stale lockfiles, schema drift, invalid globs, unsupported rule
  kinds, and repository-root mismatch
- installs explicitly selected git, Claude Code, Codex, Cursor, OpenCode,
  Devin CLI, Antigravity CLI, GitHub Copilot, and Kilo integrations
- controls autonomous agent continuation with repo-scoped `reconc run on|off`,
  prompt-scoped `/runloop` compatibility, no-progress guards, and bounded logs
- adopts typed repository TASK state and performs recoverable claim, block,
  resume, split, promotion, and archive transitions
- bounds session/report state, audit and runloop logs, generated audit binaries,
  and owned temp residue outside the Stop path
- gives agents one short remediation path with `reconc next .` and one final
  task gate with `reconc done .`

## Why Teams Use It

AI agents are useful because they can edit fast, run commands, and keep moving.
That is also the risk. Prompt instructions alone are not a control layer, and a
human review after the fact is often too late.

`reconc` gives teams policy-as-code for agentic development:

- enforce test-before-done and read-before-write contracts
- protect generated files, secrets, docs, specs, architecture boundaries, and
  release assets
- make autonomous continuation explicit with one repository switch across all
  supported agents, plus scoped prompt compatibility and no-progress guards
- give every agent the same remediation command instead of ad-hoc recovery
- leave audit-friendly decisions that can be reviewed by humans, CI, or another
  agent

## Quick Start

Build from source:

```bash
go build -o reconc ./cmd/reconc
./reconc --help
```

After installing or placing the binary on `PATH`, use `reconc` directly.

Add Reconc to a target repo:

```bash
reconc bootstrap .
```

This compatibility shorthand builds and applies a create-only minimal plan. It
scaffolds missing policy, compiles the local lockfile, selects git when `.git/`
exists, and selects registered agent platforms whose repo-local config directory
already exists. It never overwrites drift; conflicts produce review candidates.

For an existing repo, inspect evidence-backed rule and policy-pack proposals:

```bash
reconc adopt . --json
```

Pack proposals are review-only. Reconc never silently adds them to `extends`.

Then use the daily loop:

```bash
reconc status .
reconc check . --write path/to/file
reconc next .
reconc done .
reconc prune . --dry-run
```

An AI agent, not the user, operates autonomous run control:

```bash
reconc run on .
reconc run status .
reconc run off .
```

While repository mode is on, every supported runtime continues executable
typed TASK work. Explicit user interrupt or `run off` releases it immediately;
blocked, invalid, or terminal TASK state still reaches the final policy gate.
The older standalone `/runloop` prompt remains a session-scoped compatibility
switch.

Inspection and enforcement commands never mutate policy or refresh the
lockfile implicitly. If policy sources change, they fail closed with one
explicit remediation: `reconc refresh .`. Opt-in audit logging may still
append decision records.

For staged changes:

```bash
reconc ci . --staged \
  --read docs/documentation.md \
  --command-success 'go test ./...'
```

## Rollout Modes

Minimal policy and hook bootstrap:

```bash
reconc bootstrap .
```

Explicit full repo-local governance rollout:

```bash
reconc bootstrap inspect . --json
reconc bootstrap plan . --profile governed \
  --hook codex \
  --install-binary \
  --output .reconc/bootstrap-plan.json \
  --json
reconc bootstrap apply --plan .reconc/bootstrap-plan.json --json
reconc bootstrap verify --plan .reconc/bootstrap-plan.json --json
```

`inspect` and `verify` are read-only. `plan` writes only with explicit
`--output`. Packs and hooks are explicit; stack and platform detection only
suggest. Apply publishes absent targets, leaves exact files unchanged, creates
hash-addressed candidates for drift, and rolls back transaction-owned files on
failure.

Mature repositories that already own policy, agent instructions, docs, and
TASK state use `--profile existing` after `reconc refresh .`. That profile
requires a fresh lockfile and owns only explicitly selected hooks, the
repo-local wrapper, and an optional stable binary. It rejects `--pack` and
leaves every existing control-plane file untouched.

Advanced project-harness rollout:

1. Copy the Reconc toolkit into the target repository.
2. Have an agent read and follow `harness/template/BOOTSTRAP.md`.
3. Use its manual path only for project harness, stack, architecture, merge,
   and verification surfaces beyond the universal governed profile.

The versioned guide remains the AI recovery tutorial and parity checklist when
a transaction reports drift or a mature repository needs surgical adaptation.

## Supported Agent Runtimes

| Runtime | Integration |
| --- | --- |
| Claude Code | repo-local hook wiring |
| Codex | repo-local hooks with `apply_patch` path extraction |
| Cursor | pre-write, post-write, prompt, and stop hook coverage |
| OpenCode | plugin-based chat, tool, permission, and idle handling |
| Devin CLI | native session, tool, permission, stop, and post-compaction hooks |
| Antigravity CLI | invocation, tool, post-tool, and stop hook coverage |
| GitHub Copilot | repository hooks with native Copilot decision responses |
| Kilo | thin project plugin with chat, tool, permission, compaction, and idle handling |

All platforms still use git pre-commit as the hard repository backstop.

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
reconc refresh .
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
other coding agents. The skill gives every agent the same operating loop:

- check policy health before work
- collect truthful read, write, command, and claim evidence
- remediate blocks with `reconc next .`
- run `reconc done .` before claiming completion
- distinguish native hook enforcement from CLI self-checks
- operate `reconc run on|off|status` itself when autonomous TASK execution is requested

Detailed runtime behavior lives in `docs/documentation.md` and
`docs/architecture.md`.

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
- `.reconc/runloop/`
- `.reconc/task-transaction.json`
- `dist/`
- `tools/reconc/dist/`

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

`reconc` is released on the `v0.6.x` line. Core local gates pass, and release
artifacts are produced by the GitHub release workflow when a `reconc-v*` tag is
pushed.

`make self-host` builds the local binary and runs the clean-repository golden
path across all three bootstrap profiles, all nine hook platforms, TASK
lifecycle, retention, and stable release-layout binary resolution.

## License

MIT License. Copyright (c) 2026 Christopher Schulze.
