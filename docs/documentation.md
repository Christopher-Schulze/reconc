# reconc: Repository Control Compiler Documentation

This file is the source of truth for current reconc product documentation.
RFCs may remain in `docs/` as frozen contracts, but user-facing installation,
usage, architecture, release, and security facts should be kept here first.

## Contents

- [Product](#product)
- [Install And Build](#install-and-build)
- [Transactional Bootstrap](#transactional-bootstrap)
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
make self-host
make release VERSION=0.6.0
```

`make release` cross-compiles five binaries into `dist/`, generates three flat
shell-completion artifacts, generates a man page, copies the three public v1
JSON schemas, and writes `dist/SHA256SUMS`. The target stops on the first build
or checksum failure. The release verifier requires exactly those twelve
checksummed artifacts, rejects missing, extra, duplicate, unsafe, or corrupted
entries, and never accepts an empty manifest. `dist/` is ignored and should not
be committed.

`install.sh [VERSION]` downloads both the platform binary and the published
`SHA256SUMS`, requires exactly one matching SHA-256 entry, verifies the payload
before executing it, stages and re-verifies it inside the install directory,
then atomically replaces the target. A download, manifest, checksum, execution,
staging, or publication failure leaves an existing installation untouched.

## Transactional Bootstrap

New repositories use an explicit inspect, plan, apply, and verify contract:

```bash
reconc bootstrap inspect . --json
reconc bootstrap profiles --json
reconc bootstrap plan . --profile governed \
  --hook codex \
  --install-binary \
  --output .reconc/bootstrap-plan.json \
  --json
reconc bootstrap apply --plan .reconc/bootstrap-plan.json --json
reconc bootstrap verify --plan .reconc/bootstrap-plan.json --json
```

Inspection is read-only. Planning is read-only unless an explicit output path
is supplied. The `minimal` profile owns policy and a managed AI-orientation
block. The `governed` profile adds the TASK control plane, documentation,
`start.md`, runtime ignores, and the stable repo-local hook wrapper. Both use
`default` and `agent` as profile defaults. Stack detection and platform
detection produce suggestions only; packs and hooks remain explicit.

The `existing` profile is the mature-repository wiring path. It requires an
already fresh compiled policy lockfile, rejects pack selection, and owns only
explicitly selected hooks, the repo-local wrapper, and an optional stable
binary. It never owns `.reconc.yml`, agent instructions, docs, TASK files, or
ignore policy. This lets an agent install or refresh universal wiring without
forcing the governed scaffold over a repository's existing control plane.

Plans are deterministic JSON with a format version, product version, canonical
repository root, normalized selections, sorted actions, hashes, modes,
conflicts, compilation state, blocking issues, and a plan digest. Plan output
is create-only. An existing byte-identical plan is unchanged; different content
at the output path is never replaced.

Apply publishes only absent targets. Exact artifacts remain unchanged. A
different file, directory, symlink, or special target produces a
hash-addressed `.reconc-candidate-*` artifact and no normal target is installed.
A stale plan fails before publication. New files are staged beside the target,
synced, checksum-verified, and published without replacement. On failure,
rollback removes only transaction-owned files whose file identity and checksum
still match, plus transaction-created directories that are still empty.
Verification is read-only and checks artifacts, lockfile freshness, selected
hooks, governed TASK state, and selected binary resolution.

Binary installation has no network path. `--install-binary` uses the running
executable; `--binary PATH --checksum SHA256` accepts an explicit local artifact
and optional `--platform OS/ARCH`. Installed artifacts use the stable
`reconc-<os>-<arch>[.exe]` name. Resolution prefers the stable name, permits
exactly one compatible versioned fallback per searched directory, and fails on
ambiguity before trying development binaries or PATH.

`reconc bootstrap .` remains a compatibility shorthand for a create-only
minimal plan with detected hook directories. It rejects `--force`; drift must
be reviewed through candidates. The detailed AI tutorial and manual advanced
harness path remain in `harness/template/BOOTSTRAP.md`.

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
`task status`, `task validate`, `task check-done`, `run status`, `run log`,
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

TASK state uses `[ ]` for queued, `[~]` for at most one active TASK, `[!]` for
blocked, and `[x]` for done. Each detail records motivation, measurable
acceptance, sub-tasks, temporary notes, and deviations. Runtime task tracking
may assist within one session, but it never replaces these repository files.

`task_lifecycle` in `.reconc.yml` adopts the repository without migration.
`sections-v1` is the bounded canonical profile for new repositories;
`logbook-v1` accepts a `Current:` line, including `Current: none` when no TASK
is active, permanent overview rows,
and detail-file `State:` fields. `auto` selects a profile only when exactly one
grammar matches. Paths and the visible Done window are configurable. Unknown,
mixed, duplicated, unsafe, or structurally inconsistent state fails closed
with stable issue IDs and exact remediation.

`completion.required_sections` and `completion.required_evidence_fields` may
each contain at most 32 unique one-line names of at most 120 characters.
Briefings expose at most five TASK blockers, three policy gates, and six
missing evidence fields; each free-text value is capped at 240 characters and
omitted counts remain explicit.

Once `task_lifecycle` is explicitly present, its overview path is mandatory:
missing, unreadable, unsafe, or invalid TASK state fails closed instead of
degrading to `absent`. `completion.require_committed: true` additionally blocks
terminal TASK completion while the configured overview or detail tree is dirty.
The terminal gate reuses the single Git status snapshot already built for Stop;
it adds no Git process to routine executable continuations.

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

- `bootstrap` - inspect, profile, plan, apply, and verify create-only onboarding
- `status` - one-line policy health summary
- `check` - evaluate runtime evidence against compiled policy
- `next` - show the next remediation
- `done` - task-finish gate

Bootstrap and inspection:

- `bootstrap inspect|profiles|plan|apply|verify`
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
- `run`
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
- `.reconc/bootstrap-*.json`
- `*.reconc-candidate-*`

Runtime retention is product-owned rather than harness-owned. `SessionStart`
and `SessionEnd` run a cross-process-safe due check with a six-hour interval;
Stop never prunes. `reconc prune [repo] [--dry-run] [--json]` runs the same
core explicitly. Unchanged session files, active-session pointers, reports,
and run state are byte-compared and never republished. Disabled and unchanged
hook events do not create run state, and run
decisions are appended only for material transitions. Session state is
hard-capped at 1 MiB; every evidence collection has both item and byte limits,
repeated command results are deduplicated, and any omitted security-relevant
evidence sets a persisted overflow marker that blocks PreToolUse and Stop.

Default persistent budgets are 32 session files / 8 MiB / 14 days, 32 reports
/ 8 MiB / 14 days, 128 locks / 1 MiB / 24 hours, 16 MiB total external state,
and 32 MiB / 14 days for generated audit binaries. Audit and run-decision
JSONL each use a 2 MiB live file plus two archives, with file-locked append and
pre-append rotation. Repo runtime is capped at 48 MiB. Known
`reconc-proof-neg-*`, `reconc-proof-neg-copy-*`, and
`reconc-proof-gocache-*` temp trees are removed after a two-hour inactive
grace, retaining recent work while removing hard-kill residue before a full
working day passes. Active session/report/lock files, live build-lock targets,
run state/locks, and recent temp trees are never deleted to force a budget.
Global temp scanning has its own six-hour marker, so multiple repos do not
re-walk the same temp tree on every session start.

## Policy Packs And Native Assurance

Every bundled preset carries a versioned `pack` manifest with its name,
summary, stack selectors, declared capabilities, required inputs, accepted
evidence classes, implementing rule IDs, and explicit pack conflicts. Manifest
rule references and conflicts are validated before a selected pack is loaded.
User presets without a manifest remain compatible, but cannot be proposed by
stack detection and declare no capabilities.

Static command conflict analysis follows evaluator semantics:
`require_command` accepts any configured command. A `forbid_command` pair is
reported only when their exact trigger scopes overlap and that single forbid
rule blocks every required alternative. A partial overlap is satisfiable and
is not reported as a contradiction.

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
| `go_concurrency_boundary` | Changed production Go files contain no bare `go` statements without a reasoned path exemption | Matching changed Go files, parsed with the Go AST |

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
`go_concurrency_boundary` is different: it parses only changed matching `.go`
files with the Go standard-library parser and fails closed on invalid source.
It is opt-in through `go-assurance`, excluded from tests and `vendor/**` by the
bundled pack, runs no subprocess, and has zero effect on non-Go repositories.

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
- `internal/bootstrap`: deterministic inspect/plan/apply/verify transactions and binary resolution
- `internal/runtime`: policy evaluation, remediation, git integration, scripts, templates
- `internal/schema`: canonical format-versioned public JSON schema locations and enterprise URL resolution
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
- Stable schema and `format_version` fields; public v1 contracts live under `schemas/v1/` and ship in every release
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
the configuration is complete and discoverable. Separate `last_seen` and
`last_event` fields report whether a live runtime executed Reconc's
session-start route. Liveness is stored outside the repository and written at
most once per runtime every six hours, so it does not amplify tool or Stop
writes.

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
use `tools/reconc/bin/hook` on POSIX; the wrapper owns repo-local binary
selection and PATH `reconc` as last fallback. Copilot's native Windows route
uses its PowerShell command field until the cross-platform wrapper is installed.
For development and self-hosting, the wrapper checks `.build/bin/reconc` and
root `reconc` before invoking any platform probe. Otherwise, each
`tools/reconc/dist` and root `dist` directory prefers the stable platform name
and accepts exactly one compatible versioned artifact as a migration fallback.
Multiple compatible versions fail closed before PATH fallback.
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
`StrReplace`, `Delete`, and `FileEdit`, and `stop` via Cursor-native
`followup_message`. Clean Cursor
hook paths emit explicit `{"continue":true,"permission":"allow"}` JSON because
Cursor fail-closed hooks treat empty stdout as hook failure. If Cursor also
executes compatible `.claude/settings.json` hooks, Reconc detects Cursor-native
payload markers and no-ops those non-native Claude hook invocations before they
can duplicate Cursor session evidence. After compaction, Claude routes the
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
parallel run-state files or inject project-specific prompts. Their subprocess
budgets are generated from the same registry, cap output at 8 KiB, terminate
slow routes after 5, 10, or 30 seconds, and delegate versioned binary discovery
to `tools/reconc/bin/hook` instead of embedding a release number.
`reconc run on|off|status|log` is the canonical AI-operated repository switch.
Repository mode persists across sessions for Claude Code, Codex, Cursor,
OpenCode, Devin CLI, Antigravity CLI, GitHub Copilot, and Kilo Code. The agent
runs these commands itself; users do not need to operate Reconc. Prompt text,
runtime interrupts, compaction, session boundaries, runtime changes, and
application restarts never mutate the switch. An interrupt releases only the
current host invocation. `reconc run off` is the only manual disable action;
complete or absent TASK state disables it automatically after terminal gates.

Repository continuation reads the configured TASK profile through the typed
lifecycle package. An active executable TASK yields `continue`; an empty
`Current:` with queued executable work yields `claim`; blocked-only, complete,
or absent state releases Stop to the terminal policy gate; malformed or
ambiguous TASK state fails closed. Both `sections-v1` and `logbook-v1` use the
same dispositions, and the continuation prompt tells the agent to execute
`reconc task check-done`, promotion, or claim itself rather than asking the
user.

Routine executable repository continuations return before the full Stop policy
report and never spawn Git. PreToolUse, TASK mutations, pre-commit, invalid
TASK state, and terminal Stop remain hard gates. Blocked and invalid TASK state
never silently disables the durable switch; status and Stop expose the blocker
for recovery.

`awaiting_continuation` is not a hard stop reason by itself. Reads and unrelated
hook events do not clear it. A bounded material-event counter advances only for
write and command outcomes, so TASK changes or real tool progress reset the
guard without a Git dirty scan or per-tool run-state write. After repeated
no-progress stops, repository mode releases one Stop and resets its guard without
silently changing the durable switch. Run decisions are persisted only for
material state transitions in `.reconc/runloop/decisions.jsonl`, with bounded
identifiers and reasons. The live log and two archives are each bounded at
2 MiB; readers merge the ring in chronological order.
Repeated identical policy feedback shrinks to stable `RB-*` feedback IDs,
rule IDs, and the saved report path. PreToolUse evaluates only pre-execution
write/shell rules,
generated Claude/Codex/Cursor/Devin/Antigravity/Copilot configs do not spawn PreToolUse for
read-only matchers, all PostToolUse / after-shell events record evidence only,
and repo-wide policy audits run at terminal Stop, explicit Reconc checks, or a
bounded repository-run checkpoint. Checkpoints occur after 64 material events,
after 30 minutes with new material progress, or after a failed command; a clean
checkpoint records one rate-limited state transition and returns to the fast
continuation path.
Routine executable repository continuations are the bounded exception described
above; terminal Stop and explicit checks remain hard enforcement points.
Claude Code generated hooks pass
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
and Stop at normal priority. Routine executable repository continuation never
builds a Stop fingerprint. Terminal Stop uses one git status snapshot
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

- Ubuntu 24.04, macOS 15, and Windows 2025 matrix runners
- root module and `harness/template` module formatting, tidy, test, vet, pinned Staticcheck v0.7.0, and race checks
- clean-repository self-hosting golden path on Ubuntu and macOS across all three bootstrap profiles and nine hook platforms
- immutable action commit pins for checkout and Go setup
- release and installer negative-path trust tests

Release:

- Push a tag matching `reconc-v*`.
- Release workflow tests both Go modules and the trust harness before building.
- `make release VERSION=<tag-version>` builds the exact flat release inventory.
- Every artifact is verified against `SHA256SUMS` before upload.
- GitHub publication stays draft until every manifest-listed artifact and the manifest itself upload successfully.
- No Docker image is built or published.

## Git Ignore Policy

Commit:

- `.agents/hooks.json`
- `.claude/settings.json`
- `.codex/config.toml`
- `.codex/hooks.json`
- `.cursor/hooks.json`
- `.devin/hooks.v1.json`
- `.github/hooks/reconc.json`
- `.github/workflows/**`
- `.kilo/plugin/reconc.js`
- `.opencode/plugins/reconc.js`
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
- `schemas/**`
- `scripts/release/**`
- `scripts/tests/**`
- `skills/**`
- `bin/hook`
- `tools/reconc/bin/hook`

Ignore:

- `/reconc`
- `/.build/`
- `/bin/*` except `/bin/hook`
- `/dist/`
- `/tools/reconc/dist/`
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
