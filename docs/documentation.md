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
- [Policy Packs And Native Assurance](#policy-packs-and-native-assurance)
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
`task status`, `task validate`, `task check-done`, `session-briefing`,
`post-task-check`, `done`, and `tui` never compile or write
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

TASK state uses `[ ]` for queued, `[~]` for at most one active TASK, `[!]` for
blocked, and `[x]` for done. Each detail records motivation, measurable
acceptance, sub-tasks, temporary notes, and deviations. Runtime task tracking
may assist within one session, but it never replaces these repository files.

`task_lifecycle` in `.reconc.yml` adopts the repository without migration.
`sections-v1` is the bounded canonical profile for new repositories;
`logbook-v1` accepts a `Current:` line, permanent overview rows,
and detail-file `State:` fields. `auto` selects a profile only when exactly one
grammar matches. Paths and the visible Done window are configurable. Unknown,
mixed, duplicated, unsafe, or structurally inconsistent state fails closed
with stable issue IDs and exact remediation.

`completion.required_sections` and `completion.required_evidence_fields` may
each contain at most 32 unique one-line names of at most 120 characters.
Briefings expose at most five TASK blockers, three policy gates, and six
missing evidence fields; each free-text value is capped at 240 characters and
omitted counts remain explicit.

`reconc task status|validate|check-done` are read-only. `claim`, `block`,
`resume`, `split`, `promote`, and `archive` serialize through a cross-platform
lock and publish one integrity-checked transaction. `split` accepts only
pre-created child TASKs whose Why section references the parent. Promotion
checks every Sub-Task and configured evidence field before moving the detail;
it never fabricates evidence. A crash leaves `.reconc/task-transaction.json`.
All readers fail closed while that journal exists; `reconc task recover` rolls
back only if every touched path still equals its recorded before or after
image, so an external edit is never overwritten. Archived detail bodies are
not reopened by normal status or briefing reads. Runtime paths reject symlink
components, journals are capped at 4 MiB, and rollback restores the original
file bytes and permission mode.

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
- `task`
- `prune`
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
- `.reconc/runloop/`
- `.reconc/task-transaction.json`

Runtime retention is product-owned rather than harness-owned. `SessionStart`
and `SessionEnd` run a cross-process-safe due check with a six-hour interval;
Stop never prunes. `reconc prune [repo] [--dry-run] [--json]` runs the same
core explicitly. Unchanged session files, active-session pointers, reports,
and runloop state are byte-compared and never republished. Session state is
hard-capped at 1 MiB; every evidence collection has both item and byte limits,
repeated command results are deduplicated, and any omitted security-relevant
evidence sets a persisted overflow marker that blocks PreToolUse and Stop.

Default persistent budgets are 32 session files / 8 MiB / 14 days, 32 reports
/ 8 MiB / 14 days, 128 locks / 1 MiB / 24 hours, 16 MiB total external state,
and 32 MiB / 14 days for generated audit binaries. Audit and runloop decision
JSONL each use a 2 MiB live file plus two archives, with file-locked append and
pre-append rotation. Repo runtime is capped at 48 MiB. Known
`reconc-proof-neg-*`, `reconc-proof-neg-copy-*`, and
`reconc-proof-gocache-*` temp trees are removed only after a 24-hour inactive
grace. Active session/report/lock files, live build-lock targets, runloop
state/locks, and recent temp trees are never deleted to force a budget.
Global temp scanning has its own six-hour marker, so multiple repos do not
re-walk the same temp tree on every session start.

## Policy Packs And Native Assurance

Every bundled preset carries a versioned `pack` manifest with its name,
summary, stack selectors, declared capabilities, required inputs, accepted
evidence classes, implementing rule IDs, and explicit pack conflicts. Manifest
rule references and conflicts are validated before a selected pack is loaded.
User presets without a manifest remain compatible, but cannot be proposed by
stack detection and declare no capabilities.

`reconc adopt .` detects Go and Bun stack evidence and may propose
`go-assurance` or `bun-assurance`. A proposal is review-only. `adopt --apply`
adds individual rule suggestions but never mutates `extends`; the agent or user
must explicitly select a pack in `.reconc.yml` after confirming that its
contract fits the repository.

`require_assurance` is the native, no-subprocess rule kind used by assurance
packs. The parent `when_paths` controls when the gate set runs. Every gate has
an `id`, `type`, and optional `applicable_if`. Fields that do not belong to the
selected gate type are rejected instead of being silently ignored.

| Gate type | Contract | Authority surface |
|---|---|---|
| `repository_layout` | Allowed, required, forbidden, hidden, and reserved root ownership | Full repository root |
| `generated_reference` | Configured generator check has current successful command evidence | Current session |
| `language_boundary` | Changed files use configured extensions inside configured zones | Matching changed files |
| `dependency_pins` | Changed JSON dependency manifests use exact semantic versions or explicit protocol prefixes | Matching changed manifests |
| `network_boundary` | Changed source sites have a nearby non-comment guard marker or reasoned path exemption | Matching changed files |
| `process_boundary` | Changed process-spawn sites have a nearby non-comment hardening marker or reasoned path exemption | Matching changed files |
| `substantive_proof` | Fresh measured samples, computed aggregate, threshold result, live command, and byte-matched evidence agree | Full configured proof manifest |
| `live_verification` | Every or any configured command has current successful evidence | Current session |

Example:

```yaml
rules:
  - id: repository-assurance
    kind: require_assurance
    mode: block
    when_paths: ["src/**", "package.json"]
    message: Changed production surfaces must satisfy native assurance.
    assurance:
      - id: production-language
        type: language_boundary
        scan_paths: ["src/**"]
        allowed_extensions: [".go"]
        exemptions:
          - path: "src/fixtures/**"
            reason: Protocol fixtures are intentionally non-Go.
      - id: dependency-pins
        type: dependency_pins
        applicable_if: ["package.json"]
        manifest_paths: ["package.json"]
        dependency_sections: ["dependencies", "devDependencies"]
        allowed_version_prefixes: ["workspace:", "file:"]
      - id: verification
        type: live_verification
        commands: ["go test ./...", "go vet ./..."]
        command_policy: all
```

Substantive proof files use `format_version: "1"`. Each proof record requires a
unique ID, subject, current successful command, `outcome: "pass"`, aggregation
(`last`, `mean`, `min`, `max`, `median`, or `p95`), comparator (`lt`, `lte`,
`eq`, `gte`, or `gt`), numeric threshold and actual, measured samples, an
RFC3339 verification time, and a repository-relative evidence path plus its
SHA-256. Reconc recomputes the aggregate from the samples, compares it to both
the declared actual and threshold, checks freshness, reruns no command itself,
and verifies the evidence bytes.

Native assurance is intentionally bounded: 20,000 changed paths, 4,096 unique
files, 4 MiB per file, 32 MiB total reads, 50,000 applicability or reserved-dir
walk entries, and 50 returned findings plus one explicit omitted-count marker.
An unreadable or over-budget authority surface is an error and fails closed.
Matching gates reuse one canonical path resolution and one bounded in-memory
file snapshot per evaluation, so overlapping source gates do not reread the
same bytes from the SSD.
Network and process gates are deterministic source heuristics, not semantic AST
proofs; select narrow site patterns and guard markers, and use explicit
reasoned exemptions where language-specific control flow cannot be expressed.

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
- `internal/assurance`: bounded native repository assurance evaluators
- `internal/hooks`: typed hook platform registry, artifact generation, non-destructive install, scaffold sync, and activation diagnostics
- `internal/runtime/agentsession`: hook-runtime session state and event handling
- `internal/audit`: opt-in JSONL decision log and rotation
- `internal/atomicfile`: atomic write-on-change publication
- `internal/filelock`: Unix/Windows cross-process file locking
- `internal/jsonl`: bounded, locked JSONL append and archive rings
- `internal/retention`: runtime storage classes, lifecycle due checks, and cleanup
- `internal/presets`: bundled and user policy packs
- `internal/templates`: bundled and user rule templates
- `internal/tasklifecycle`: typed TASK profiles, validation, bounded briefing,
  recoverable transactions
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

The typed platform registry is the source of truth for Git pre-commit, Claude
Code, Codex, Cursor, OpenCode, Devin CLI, Antigravity CLI, GitHub Copilot, and
Kilo. It owns native event names, normalized lifecycle coverage, compatibility
routes, config and scaffold paths, failure behavior, timeout budgets, output
budgets, installation strategy, and activation probes. `reconc hook status
[repo] [--json]` validates every registered artifact and reports `absent`,
`installed`, `active`, `degraded`, `shadowed`, or `unsupported`. `active` means
the configuration is complete and discoverable; it does not claim that a live
agent process already loaded it.

The registry assigns 5-second observation/session budgets, 10-second pre-tool
and permission budgets, and 30-second Stop budgets instead of one blanket
timeout. Claude, Devin, Antigravity, and Copilot generators emit those host
timeouts; OpenCode and Kilo enforce them inside their adapters. Each runtime
route caps combined process output at 8 KiB.
Post-compaction recovery context is deduplicated and capped at 4 KiB.
Copilot's `PreCompact` event is intentionally not installed because that event
ignores output, so spawning Reconc there would add latency without restoring
context.

Claude Code, Codex, Cursor, Devin, Antigravity, and Copilot generated configs
use `tools/reconc/bin/hook` on POSIX; the wrapper owns repo-local dist-binary
selection and PATH `reconc` as last fallback. Copilot's native Windows route
uses its PowerShell command field until the cross-platform wrapper is installed.
Claude Code uses its exec-form
`command`+`args` shape so it does not spawn a hook shell or run a hook-launcher
Git lookup. Codex uses the host shell command string without a nested `sh -lc`;
Cursor, Antigravity, and Copilot use portable shell launchers with a direct
wrapper fast path before their Git fallback.
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
can mutate Cursor session or Runloop state. After compaction, Claude routes the
context-capable `SessionStart` `compact` matcher through Reconc; it does not
spawn the notification-only `PostCompact` event. Devin uses
`.devin/hooks.v1.json`, including `PostCompaction`, and suppresses compatible
Claude-hook duplicates. GitHub Copilot uses `.github/hooks/reconc.json` version
1 with VS Code-compatible payloads and Copilot-native decision JSON.
Antigravity uses `.agents/hooks.json` with `PreInvocation`, `PreToolUse`,
`PostToolUse`, `PostInvocation`, and `Stop`; Reconc stores Antigravity PreTool
metadata as pending evidence so PostToolUse can record exact evidence when the
post payload only carries a step index/result. OpenCode and Kilo use thin Bun
adapters at `.opencode/plugins/reconc.js` and `.kilo/plugin/reconc.js`. They
translate host events only; policy, session state, compaction context, and
continuation decisions stay in the Go runtime, so the plugins do not maintain
parallel runloop files or inject project-specific prompts. Their subprocess
budgets are generated from the same registry, cap output at 8 KiB, terminate
slow routes after 5, 10, or 30 seconds, and delegate versioned binary discovery
to `tools/reconc/bin/hook` instead of embedding a release number.
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
output. The live log and two archives are each bounded at 2 MiB; readers merge
the ring in chronological order.
Repeated identical policy feedback shrinks to stable `RB-*` feedback IDs,
rule IDs, and the saved report path. PreToolUse evaluates only pre-execution
write/shell rules,
generated Claude/Codex/Cursor/Devin/Antigravity/Copilot configs do not spawn PreToolUse for
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
globally serialized, so parallel results cannot overwrite each other. Runtime
retention no longer piggybacks on audit-cache publication. The task-state cache hashes only
`docs/tasks.md`, schema, and open TASK bodies on its hot path. A clean completed
TASK archive is represented by its committed Git tree ID plus directory
metadata; dirty or unreadable archive state bypasses caching entirely, avoiding
full archive reads without hiding archived-file changes. Reproducible Stop and
concurrent-cache benchmarks live beside their regression tests and run with
`go test ./internal/runtime/agentsession -run '^$' -bench StopPolicy -benchmem`
and `go test ./harness/template/audits -run '^$' -bench RunWithCache -benchmem`.
Storage hot paths run with
`go test ./internal/runtime/agentsession ./internal/retention -run '^$' -bench 'DuplicateSessionMutation|LifecycleRetentionNotDue' -benchmem`.
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
- `.reconc/task-transaction.json`

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
