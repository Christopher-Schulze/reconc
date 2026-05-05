# reconc: Repository Control Compiler Documentation

This file is the source of truth for current reconc product documentation.
RFCs may remain in `docs/` as frozen contracts, but user-facing setup, usage,
architecture, release, and security facts should be kept here first.

## Contents

- [Product](#product)
- [Install And Build](#install-and-build)
- [Daily Workflow](#daily-workflow)
- [Command Surface](#command-surface)
- [Repository Policy](#repository-policy)
- [Architecture](#architecture)
- [Agent Skill](#agent-skill)
- [GitHub And Release](#github-and-release)
- [Git Ignore Policy](#git-ignore-policy)
- [Security](#security)
- [License](#license)
- [Documentation Rules](#documentation-rules)
- [Release State](#release-state)

## Product

`reconc` is the Repository Control Compiler. It compiles repository policy
from `AGENTS.md`, `.reconc.yml`, presets, templates, and policy files into a
local policy lockfile, then evaluates runtime evidence, agent hook events, and
git-derived diffs against that deterministic contract.

The product is a standalone Go CLI. It does not require Docker, Node, Python,
or a local service. Runtime behavior should stay offline by default.

## Install And Build

Requirements:

- Go `1.26`
- Git for `reconc ci` and hook installation

Common commands:

```bash
go test ./...
go test -race -count=1 ./...
go vet ./...
go build ./cmd/reconc
go run ./cmd/reconc --help
go run ./cmd/reconc compile .
go run ./cmd/reconc doctor . --deep
```

Make targets:

```bash
make build
make test
make vet
make lint
make cover
make bench
make release VERSION=0.4.0
```

`make release` cross-compiles five binaries into `dist/`, generates shell
completion scripts, generates a man page, and writes `dist/SHA256SUMS`.
`dist/` is ignored and should not be committed.

## Daily Workflow

Most users should use this path:

```bash
reconc setup .
reconc status .
reconc check . --write path/to/file
reconc next .
reconc done .
```

Exit codes:

- `0`: pass, warn, or informational success
- `1`: runtime or input error
- `2`: blocking policy violation

## Command Surface

Daily:

- `setup` - friendly bootstrap alias for new repos
- `status` - one-line policy health summary
- `check` - evaluate runtime evidence against compiled policy
- `next` - show the next remediation
- `done` - task-finish gate

Setup and inspection:

- `init`
- `bootstrap`
- `adopt`
- `extract`
- `doctor`
- `verify`

Compile and evaluate:

- `compile`
- `ci`
- `assert`
- `can`
- `diff`
- `watch`

Explain and remediate:

- `explain`
- `fix`
- `why`

Packs and wiring:

- `preset`
- `template`
- `hook`

Workflow maintenance:

- `changelog`
- `agent-intro`
- `audit`
- `session-briefing`
- `context`
- `start`
- `post-task-check`
- `delta`
- `spec`
- `coverage`
- `tui`
- `completion`
- `manpage`
- `version`

For exact flags, run `reconc <command> --help`.

## Repository Policy

Repo-local policy lives in `.reconc.yml`. It should be committed.

The generated lockfile is `.reconc/policy.lock.json`. It is intentionally
ignored in this repository because the current lockfile format records absolute
local paths in `repo_root` and `discovery.start_path`. Contributors should
regenerate it locally with:

```bash
reconc compile .
```

Runtime state is local and ignored:

- `.reconc/.compile.lock`
- `.reconc/audit.jsonl`
- `.reconc/audit.jsonl.*`
- `.reconc/sessions/`
- `.reconc/reports/`

## Architecture

Pipeline:

```text
repo root -> ingest -> parser -> compiler -> .reconc/policy.lock.json -> runtime -> CheckReport/FixPlan
```

Package responsibilities:

- `cmd/reconc`: CLI entry point only
- `internal/cli`: argument parsing and command dispatch
- `internal/ingest`: repository discovery and source loading
- `internal/parser`: YAML-to-policy validation and normalization
- `internal/compiler`: canonical JSON lockfile generation, digesting, conflicts, migrations, compile lock
- `internal/runtime`: policy evaluation, remediation, git integration, scripts, templates
- `internal/hooks`: git, Claude Code, and Codex hook generation and install
- `internal/runtime/agentsession`: hook-runtime session state and event handling
- `internal/audit`: opt-in JSONL decision log and rotation
- `internal/presets`: bundled and user policy packs
- `internal/templates`: bundled and user rule templates
- `internal/tui`: dependency-free terminal dashboard

Key invariants:

- Deterministic JSON artifacts
- Stable schema and `format_version` fields
- Fail closed on malformed policy, stale lockfiles, schema drift, invalid globs, unsupported rule kinds, and lockfile root mismatch
- No runtime network calls
- Behavior in internal packages, thin `cmd/reconc/main.go`

## Agent Skill

The repo ships one agent-facing skill at `skills/reconc/SKILL.md`.

It is written for Codex, OpenCode, Claude Code, and other coding agents. The
skill documents the same reconc workflow for every agent runtime:

- check policy health before work
- collect truthful read, write, command, and claim evidence
- use `reconc next .` for remediation
- run `reconc done .` before claiming completion
- distinguish native hook enforcement from CLI self-checks

Claude Code and Codex have native hook wiring. OpenCode and other agents use
the same CLI loop plus git pre-commit as the repository backstop.

## GitHub And Release

GitHub workflows:

- `.github/workflows/reconc-ci.yml`
- `.github/workflows/reconc-release.yml`

CI checks:

- formatting
- `go mod tidy -diff`
- `go test ./...`
- `go vet ./...`
- `make lint`
- `go test -race -count=1 ./...`

Release:

- Push a tag matching `reconc-v*`.
- Release workflow builds artifacts with `make release VERSION=<tag-version>`.
- Checksums are verified before upload.
- No Docker image is built or published.

## Git Ignore Policy

Commit:

- `.github/workflows/**`
- `.gitignore`
- `.reconc.yml`
- `AGENTS.md`
- `LICENSE`
- `Makefile`
- `README.md`
- `SECURITY.md`
- `cmd/**`
- `docs/documentation.md`
- `docs/architecture.md`
- `docs/commands.md`
- `docs/rfcs/**`
- `go.mod`
- `go.sum`
- `install.sh`
- `internal/**`
- `skills/**`

Ignore:

- `/reconc`
- `/bin/`
- `/dist/`
- `*.test`
- `*.out`
- `coverage.out`
- `coverage.html`
- `.DS_Store`
- `.vscode/`
- `.idea/`
- `*.swp`
- `tmp/`
- `/CHANGELOG.md`
- `/changelog.md`
- `/CHANGES.md`
- `/bench-baseline.txt`
- `/todo.md`
- `/todo/`
- `/docs/todo.md`
- `/docs/todo/`
- `/docs/changelog.md`
- `/docs/changelog/`
- `/docs/security-review-*.md`
- `/docs/*audit*.md`
- `/docs/pilot-*.md`
- `/docs/parity-audit-*.md`
- `/docs/pilot-prep-*.md`
- `.reconc/policy.lock.json`
- `.reconc/.compile.lock`
- `.reconc/audit.jsonl`
- `.reconc/audit.jsonl.*`
- `.reconc/sessions/`
- `.reconc/reports/`

## Security

Security posture:

- Agent payloads are untrusted input.
- Hook runtime payloads are size and depth bounded.
- Paths are normalized and constrained to the discovered repository root.
- Payload command strings are matched as data and are not executed.
- Only policy-authored `require_script` entries execute subprocesses.
- Audit log is opt-in via `RECONC_AUDIT=1`.
- Lockfile root mismatch is a hard stale/fail condition.

Security reports should be private first and include the command, policy,
lockfile shape, payload if relevant, and reproduction steps.

## License

`reconc` is distributed under the MIT License.

Copyright (c) 2026 Christopher Schulze.

## Documentation Rules

`docs/documentation.md` is the current documentation SSOT.

Allowed supporting docs:

- `docs/rfcs/**` for frozen contracts
- `README.md` as the GitHub landing page
- `SECURITY.md` as security policy

Local planning and release-note scratch files such as `todo.md`,
`docs/todo/**`, and `CHANGELOG.md` are ignored in this repository. When
behavior changes, update `docs/documentation.md` first. Supporting docs may
link to it, but should not become competing current-state documentation.

## Release State

The current public release line is `v0.4.x`. Core tests, race tests, vet,
staticcheck, coverage, doctor, verify, and release artifact generation pass
locally. Release artifacts are produced by the GitHub release workflow when a
`reconc-v*` tag is pushed.
