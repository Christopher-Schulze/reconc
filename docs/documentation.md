# reconc: Repository Control Compiler Documentation

This file is the source of truth for current reconc product documentation.
RFCs may remain in `docs/` as frozen contracts, but user-facing installation,
usage, architecture, release, and security facts should be kept here first.

## Contents

- [Product](#product)
- [Install And Build](#install-and-build)
- [Daily Workflow](#daily-workflow)
- [Development Control Plane](#development-control-plane)
- [Minimal Example Policy](#minimal-example-policy)
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

`reconc` does not make LLM output deterministic. It makes the repository control
surface around agent work deterministic: write boundaries, required evidence,
runtime continuation decisions, audit trails, and fail-closed policy gates.

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
go run ./cmd/reconc refresh .
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
make release VERSION=0.6.0
```

`make release` cross-compiles five binaries into `dist/`, generates shell
completion scripts, generates a man page, and writes `dist/SHA256SUMS`.
`dist/` is ignored and should not be committed.

## Daily Workflow

Most users should use this path:

```bash
reconc bootstrap .
reconc status .
reconc check . --write path/to/file
reconc next .
reconc done .
```

`status`, `doctor`, `verify`, `check`, `ci`, `assert`, `can`, `why`,
`session-briefing`, `post-task-check`, `done`, and `tui` never compile or write
the lockfile. Missing, stale, malformed, schema-drifted, or wrong-root
lockfiles fail closed with one explicit remediation: `reconc refresh .`.
When `RECONC_AUDIT=1`, enforcement commands may still append decision records;
that opt-in audit write is independent of policy refresh.

Exit codes:

- `0`: pass, warn, or informational success
- `1`: runtime or input error
- `2`: blocking policy violation

## Development Control Plane

Product implementation work is tracked in `docs/tasks.md`. It is the durable
control plane for current work and links to one detail file per TASK under
`docs/tasks/`. Completed details move to `docs/tasks/done/`; the overview keeps
only the ten newest completed TASKs visible.

TASK state uses `[ ]` for queued, `[~]` for the single active TASK, `[!]` for
blocked, and `[x]` for done. Each detail records motivation, measurable
acceptance, sub-tasks, temporary notes, and deviations. Runtime task tracking
may assist within one session, but it never replaces these repository files.

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

Run:

```bash
reconc refresh .
reconc check . --write internal/example.go
reconc check . --write internal/example.go --command-success 'go test ./...'
```

The first command explicitly compiles policy into the local lockfile. The
second command shows that a protected source write needs test evidence. The
third command supplies that evidence.

## Command Surface

Daily:

- `bootstrap` - minimal init + compile + hook bootstrap for new repos
- `status` - one-line policy health summary
- `check` - evaluate runtime evidence against compiled policy
- `next` - show the next remediation
- `done` - task-finish gate

Bootstrap and inspection:

- `init`
- `adopt`
- `extract`
- `doctor`
- `verify`

Compile and evaluate:

- `compile`
- `refresh`
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
- `runloop`
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
reconc refresh .
```

Runtime state is local and ignored:

- `.reconc/.compile.lock`
- `.reconc/audit.jsonl`
- `.reconc/audit.jsonl.*`
- `.reconc/cache/`
- `.reconc/locks/`
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
- `internal/hooks`: git, Claude Code, and Codex hook generation and install, including PermissionRequest wiring
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

Claude Code, Codex, Cursor, OpenCode and Antigravity have repo-local
prompt/tool/stop hook wiring through generated configs that call
`tools/reconc/bin/hook`; the wrapper owns repo-local dist-binary selection and
PATH `reconc` as last fallback. Claude Code uses its exec-form `command`+`args`
shape so it does not spawn a hook shell or run a hook-launcher Git lookup.
Codex uses the host shell command string without a nested `sh -lc`; Cursor and
Antigravity keep their portable shell launcher until their direct argv shape is
proven by runtime docs/tests.
Codex also needs `hooks = true` in an active `config.toml` and routes
`apply_patch` through Reconc by parsing patch headers from
`tool_input.command`. Cursor Desktop uses `.cursor/hooks.json` with
`preToolUse` as the pre-write gate, `afterFileEdit`/`afterTabFileEdit` plus
`postToolUse` as evidence backstops for Cursor write aliases including
`StrReplace`, `Delete`, and `FileEdit`, `beforeSubmitPrompt` for standalone
`/runloop`, and `stop` via Cursor-native `followup_message`. Clean Cursor
hook paths emit explicit `{"continue":true,"permission":"allow"}` JSON because
Cursor fail-closed hooks treat empty stdout as hook failure. If Cursor also
executes compatible `.claude/settings.json` hooks, Reconc detects Cursor-native
payload markers and no-ops those non-native Claude hook invocations before they
can mutate Cursor session or Runloop state. OpenCode uses
`.opencode/plugins/reconc.js`
with `chat.message`, `tool.execute.*`, `permission.ask`, and `session.idle`;
Antigravity uses `.agents/hooks.json` with `PreInvocation`, `PreToolUse`,
`PostToolUse`, `PostInvocation`, and `Stop`; Reconc stores Antigravity
PreTool metadata as pending evidence so PostToolUse can record exact
read/write/command evidence even when the post payload only carries the step
index/result.
Runloop activation is prompt-only and requires a standalone `/runloop`
slash-command flag in sanitized real user prompt text, so quoted transcripts,
multi-line quoted chat blocks, pasted transcript marker lines, diagnostic
mention lines such as "kein /runloop", pure hook prompts, stop feedback, code
fences, tool text, and errors cannot start it accidentally. Runtime-internal
continuation prompts are accepted only when they are the control payload itself
(for example a pure `<hook_prompt>...</hook_prompt>` block or a prompt starting
with the generated autocontinue text), so a normal user diagnostic that merely
mentions `runloop autocontinue` still counts as a real non-Runloop prompt
and stops the active same-runtime run.
Runloop runs are session- and runtime-scoped: a normal same-session prompt
stops that run, except a same-session `/btw` side-channel prompt, which
preserves the active run and must not write `.reconc/runloop/stop`;
prompts, interrupts, session ends, or stop markers from another agent runtime
or session in the same repo must not stop the active run.
`.reconc/runloop/stop` is scoped to the active run and agent runtime and clears when a new
standalone `/runloop` prompt starts a run. Active Runloop Stop events use a
pre-policy continuation fast path when the session has no Stop-time evidence, or
when a runtime re-enters with `stop_hook_active=true` and the cached policy
report is already clean for the exact same Stop-time evidence hash;
evidence-bearing stops still run the policy gate first, and changed evidence
invalidates the clean-cache path, so blocking policy reports win over
Runloop.
`awaiting_continuation` is not a hard stop reason by itself; if a runtime
bounces through another Stop before visible tool progress, Reconc may re-emit
the continuation prompt until progress or the no-progress guard decides. Tool
events clear `awaiting_continuation`, so the no-progress guard resets from
hook-observed work without running a full Git dirty scan on every Runloop
continuation.
Runloop decisions are persisted in `.reconc/runloop/decisions.jsonl` with
branch/runtime/session/state fields for forensic debugging without bloating hook
output.
Repeated identical policy blocks stay blocking but shrink to rule IDs plus the
saved report path. PreToolUse evaluates only pre-execution write/shell rules,
generated Claude/Codex/Cursor/Antigravity configs do not spawn PreToolUse for
read-only matchers, all PostToolUse / after-shell events record evidence only,
and repo-wide policy audits run only at Stop or explicit Reconc checks. Stop and
explicit checks remain the hard enforcement points. Claude Code generated hooks pass
`${CLAUDE_PROJECT_DIR}` to the repo-local wrapper as argv. Shell-command
runtimes first exec `./tools/reconc/bin/hook` directly when their cwd is already
the repo root, and only fall back to `git rev-parse` plus
`RECONC_HOOK_REPO_RESOLVED=1` when needed. The agent-hooks audit rejects
git-first launchers, Claude shell/git launchers and wrapper configs that omit
the direct-wrapper fast path. The wrapper trusts either the resolved marker or
an already-valid repo-local wrapper/dist path, normalizes only
direct/manual calls, and `exec`s the selected Reconc binary so no avoidable shell
parent remains;
the Go hook runtime lowers observation-only events (`post/after/session-end`)
with best-effort Unix process priority while keeping PreToolUse, permission,
and Stop at normal priority. The Stop fingerprint uses one git status snapshot
per report build with default `--untracked-files=normal`, dirty-path
content/index hashes, direct loose/packed/worktree HEAD resolution, and a
per-session report lock instead of full `git diff --binary` output or repeated
status walks. The same bounded status snapshot scopes Stop-time write evidence
to paths that are both session-recorded and still uncommitted; unknown Git or
path state keeps the full session write set and therefore fails closed. The
completed report is cached under that initial fingerprint and the exact
read/write/command/claim evidence hash. Normal Stops still rebuild the
fingerprint, while reentrant `stop_hook_active=true` calls may reuse a clean
cached report only when both the full repo fingerprint and evidence hash still
match, so the next Stop reruns if the repo or evidence changes after the report
was built. Alternate Git ref backends fall back to `git rev-parse`; the normal
path avoids that extra process. Reconc's own `.reconc/cache/`,
`.reconc/runloop/`, `.reconc/locks/`, `.reconc/reports/`, and
`.reconc/audit.jsonl` runtime artefacts are excluded from the dirty fingerprint
so report writes cannot invalidate their own cache. `RECONC_STOP_FINGERPRINT_UNTRACKED=all`
restores the old all-untracked cache key for repos that need it. Matching `require_script` rules
that call the same `run-workflow-audit` runner are batched through
`--batch-json` in one process and then split back into per-rule pass/block
reports, so subprocess startup drops without weakening rule attribution. All
runtimes still keep git pre-commit as the repository backstop.
The runtime keeps the old read-safe fast path as defense in depth if a host tool
still sends a read-only PreToolUse event; write tools still resolve the repo and
fail closed before policy evaluation. Payload parsing stays allocation-light by decoding
directly from bytes, and duplicate Cursor-payload suppression uses a cheap
marker prefilter before JSON decoding. `RECONC_HOOK_TIMING=1` or
`RECONC_HOOK_TIMING_THRESHOLD_MS=<ms>` emits payload/read/handler/adapt timing
to stderr for hook latency diagnosis.
Require-script subprocesses run in their own process group; on timeout Reconc
sends SIGTERM to the group and escalates to SIGKILL after the configured kill
grace period, so shell grandchildren such as `go build` compiler workers cannot
survive as orphans after a blocked hook. Workflow-audit launchers build their
cached binaries behind an atomic mkdir build lock and publish via temp binary +
rename; parallel agent hooks therefore wait for one rebuild instead of stampeding
the Go compiler or exposing a partially written cache binary.
Independent cold workflow-audit keys execute concurrently behind per-key
singleflight locks. Only short cache read/merge/atomic-publication sections are
globally serialized, so parallel results cannot overwrite each other and prune
I/O never holds the shared cache lock. The task-state cache hashes only
`docs/tasks.md`, schema, and open TASK bodies on its hot path. A clean completed
TASK archive is represented by its committed Git tree ID plus directory
metadata; dirty or unreadable archive state bypasses caching entirely, avoiding
full archive reads without hiding archived-file changes. Reproducible Stop and
concurrent-cache benchmarks live beside their regression tests and run with
`go test ./internal/runtime/agentsession -run '^$' -bench StopPolicy -benchmem`
and `go test ./harness/template/audits -run '^$' -bench RunWithCache -benchmem`.
Harnesses can also expose an `agent-quality` mode for objective live-diff
quality gates: newly added test skips, placeholder completion language,
untested sensitive Go edits, and stale live Reconc binaries can block without
retroactively failing untouched legacy code.
Line counting in the workflow-audit harness (`lineCount`) follows `wc -l`/editor
semantics: a trailing newline terminates the final line and does not add a
phantom extra line, so spec-line-count and spec-line-range gates (for example
the spec-code-parity audit `Spec Line Count` check) match the real file length.

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
- `docs/tasks.md`
- `docs/tasks/**`
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
- `.reconc/cache/`
- `.reconc/locks/`
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
- `docs/tasks.md` and `docs/tasks/**` for the implementation control plane and completed TASK history
- `README.md` as the GitHub landing page
- `SECURITY.md` as security policy

Local planning and release-note scratch files such as `todo.md`,
`docs/todo/**`, and `CHANGELOG.md` are ignored in this repository. When
behavior changes, update `docs/documentation.md` first. Supporting docs may
link to it, but should not become competing current-state documentation.

## Release State

The current public release line is `v0.6.x`. The universal evolution program is
tracked in `docs/tasks.md`; a new release is blocked until its release, install,
self-hosting, and final verification contracts pass. Release artifacts are
produced by the GitHub release workflow when a `reconc-v*` tag is pushed.
