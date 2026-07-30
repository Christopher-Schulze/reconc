<p align="center">
  <img src="assets/reconc.png" alt="Reconc: AI agents say they're done. Reconc proves it." width="100%">
</p>

# Reconc

**Evidence-bound completion control for AI coding agents.**

Reconc is a Repository Control Compiler: an offline, deterministic control and
evidence layer that turns project instructions into executable gates and
refuses completion until the repository, current evidence, and TASK state
agree.

[![CI](https://github.com/Christopher-Schulze/reconc/actions/workflows/reconc-ci.yml/badge.svg)](https://github.com/Christopher-Schulze/reconc/actions/workflows/reconc-ci.yml)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)
[![Offline](https://img.shields.io/badge/runtime-offline_by_default-111827)](#what-reconc-controls)

[Why Reconc](#why-reconc-exists) · [Install](#install-and-bootstrap) · [Architecture](#architecture-and-operational-boundaries) · [Integrations](#supported-agent-runtimes) · [Documentation](docs/documentation.md) · [Security](SECURITY.md) · [Contributing](CONTRIBUTING.md) · [Issues](https://github.com/Christopher-Schulze/reconc/issues/new/choose) · [Releases](https://github.com/Christopher-Schulze/reconc/releases)

An agent saying "done" is a claim. Reconc verifies that claim against the
current repository, policy, evidence, and TASK state. Missing tests, stale
proof, protected edits, documentation drift, unfinished work, and stuck loops
become deterministic decisions with one exact next action.

No second model judges the first. One local Go binary compiles repository-owned
rules and enforces them across the CLI, Git, CI, and ten coding-agent
integrations. The exact
[guarantees and limits](docs/documentation.md#evidence-bound-completion-control)
stay explicit.

## Why Reconc Exists

Coding agents can edit large surfaces, run many tools, cross session
boundaries, and produce convincing completion prose. The repository still has
to answer harder questions:

- Did the agent stay inside the approved scope?
- Did it read the governing instructions before changing code?
- Did the required test run after the final relevant write?
- Does the staged candidate match the state that was actually verified?
- Is the active TASK genuinely complete, or only described as complete?
- Did a runtime hook really execute, or is it merely configured?
- Can a reviewer inspect the final evidence without receiving private prompts
  or session transcripts?

Tests alone answer only part of that. A test runner does not normally know
whether its result is stale, whether protected files changed, whether the task
ledger is valid, whether a previous blocking decision remains unresolved, or
whether the candidate presented for completion is the candidate that passed.

Reconc treats completion as a repository state transition, not a sentence. It
compiles the project's own rules into a deterministic contract, records
bounded evidence at supported boundaries, ties successful verification to the
state it observed, and rejects completion when those facts disagree.

| Existing control | What it proves | What Reconc adds |
| --- | --- | --- |
| Test runner | A command returned a result for some state. | Causal freshness and candidate identity for the state being judged. |
| Git hook | A configured local hook ran at one Git boundary. | The same policy model across agent hooks, CLI checks, staged CI, TASK state, and completion. |
| CI status | A remote workflow reported success for a commit. | Repository-local policy, exact staged proof, unresolved-block handling, and portable completion evidence. |
| Agent instructions | The agent was told what to do. | Compiled, reviewable, fail-closed enforcement for rules that can be evaluated deterministically. |
| Second model review | Another probabilistic system formed an opinion. | A local deterministic decision over current configured evidence. |

Reconc is useful when repositories need stronger control over autonomous or
semi-autonomous coding work without replacing their existing tests, linters,
build systems, CI, or human review. It composes those systems into a stricter
completion boundary.

## How Reconc Works

**Repository rules** → **compiled policy lock** → **CLI, Git, CI, and agent
hooks** → **pass, warn, or block with one exact next action**

`reconc` does not make LLMs deterministic. It makes the boundary around their
work deterministic: which files are protected, which commands must have run,
which claims must be supplied, which hook events are allowed to continue, and
why a task is allowed to be called done.

The control flow has four stages:

1. **Compile repository intent.** Reconc ingests `AGENTS.md`, `.reconc.yml`,
   policy packs, templates, and repository-owned files. `reconc refresh .`
   emits a portable `.reconc/policy.lock.json` with deterministic ordering,
   provenance, schema identity, source digest, and lock digest.
2. **Observe real activity.** CLI commands and installed runtime adapters
   normalize reads, writes, commands, outcomes, claims, Git state, TASK state,
   and selected MCP effects into bounded evidence. Raw prompts and model
   transcripts are not required for the policy engine.
3. **Evaluate one contract.** The same decision engine is used by explicit
   checks, agent hooks, git pre-commit, staged CI, autonomous Stop handling,
   and final completion. Decisions are `pass`, `warn`, or `block`, with stable
   exit codes and one actionable remediation.
4. **Bind completion to the candidate.** `reconc done .` verifies the current
   policy, Git candidate, evidence, unresolved decisions, command proofs, and
   typed TASK state. `reconc proof .` exports that result as portable JSON or
   Markdown for review.

Core invariants are deliberately strict:

- Inspection never refreshes policy implicitly. A stale or malformed lockfile
  fails closed and names `reconc refresh .` as the explicit repair.
- A successful command is not timeless. Relevant later writes invalidate
  session evidence, and staged proofs are valid only for the exact HEAD and
  index they verified.
- Bootstrap, repository sync, update, uninstall, TASK mutation, and generated
  output use ownership records and transactional publication instead of
  overwriting ambiguous bytes. TASK publication revalidates exact bytes, file
  modes, and move destinations and never clobbers an existing archive path.
- Static configuration is not live proof. Hook status reports configuration
  and per-route observations separately.
- Core policy compilation, evaluation, hooks, run control, repository sync,
  and proof generation make no network calls.

## What Reconc Controls

| Control surface | What Reconc provides |
| --- | --- |
| Policy compiler | Compiles repository instructions, YAML policy, packs, templates, and provenance into a portable lockfile. Unknown fields, stale sources, schema drift, invalid globs, unsupported rule kinds, and non-portable current roots fail closed. |
| Scope and change control | Records and evaluates reads, writes, commands, claims, protected paths, coupled changes, generated files, secret state, destructive commands, and out-of-scope edits or deletions. |
| Evidence freshness | Binds command success to the write epoch or staged Git candidate it verified. A later relevant write invalidates earlier success instead of laundering stale proof. |
| Completion | `reconc done .` accepts completion only when policy, HEAD, index, worktree, evidence, reports, unresolved blocks, staged proofs, and typed TASK state agree. |
| Portable proof | `reconc proof .` exports the current completion candidate as deterministic JSON or Markdown while excluding private session material and raw command arguments. |
| Native assurance | Runs bounded gates for repository layout, language boundaries, dependency pins, package scripts, source hygiene, formatting, concurrency, process and network boundaries, substantive proof, and live verification. |
| TASK continuity | Validates and mutates typed TASK state with recoverable claim, block, resume, split, promote, archive, and transaction recovery operations. |
| Autonomous run control | `reconc run on|off|reset|status|log` provides one durable repository switch, bounded decision logs, terminal gates, and per-session no-progress guards. |
| Transactional adoption | Inspects existing repositories, proposes evidence-backed packs and commands, and plans, applies, verifies, synchronizes, or removes only receipt-owned rollout state. |
| Runtime enforcement | Generates, installs, verifies, and safely removes registry-backed hooks for ten coding-agent runtimes, with capability-specific failure semantics and git pre-commit as the repository backstop. |
| MCP side-effect control | Classifies explicitly configured Cursor, OpenCode, and Kilo MCP tools as repository reads, writes, commands, or external effects using exact selectors and fail-closed extraction. |
| Operator and CI tooling | Provides exact remediation, body-free source-provenance inspection, staged command execution, CI proofs, global diagnostics, update and uninstall, cryptographically verified audit inspection, retention, TUI, shell completions, and a generated manpage. |
| Release trust | Publishes strict release manifests, SHA-256 checksums, build-provenance attestations, and deterministic SPDX 2.3 and CycloneDX 1.6 SBOMs tied to the release commit. |

## Evidence Model

Reconc separates different kinds of evidence instead of treating every green
signal as equivalent.

| Evidence class | Bound to | Accepted use |
| --- | --- | --- |
| Session read/write evidence | Canonical repository and active session | Policy checks that require observed context or scope activity. |
| Command outcome | Normalized command, exit status, session, and causal write epoch | Runtime rules such as `require_command_success` while the relevant state remains current. |
| Staged command proof | Exact HEAD, index, worktree cleanliness, command, and expiry contract | `reconc ci . --staged` and final candidate verification. |
| Claim | Named assertion and session | Policies that explicitly accept that claim class. A claim never becomes command proof automatically. |
| TASK state | Configured overview/detail grammar and current repository files | Continuation, transition, and final-completion decisions. |
| Hook liveness | Runtime, exact route, bounded timestamp, and repository identity | Distinguishing configured integration from a route actually observed in use. |
| Completion report | Policy, candidate fingerprint, evidence, checks, and TASK disposition | Local final gate and source data for portable proof export. |

Evidence is causal, bounded, and reload-safe:

- Session writes advance causal epochs. A test recorded before a later
  matching source write no longer satisfies a success requirement.
- `reconc exec . --staged -- COMMAND` runs the real command and publishes proof
  only when HEAD, index, and worktree remain unchanged for the whole run.
- Explicit blocking decisions remain unresolved for the same candidate until a
  later explicit non-blocking evaluation clears them. Time alone never turns a
  block into a pass.
- Large sessions seal evidence into SHA-256-linked segments rather than
  silently truncating the history. Missing, corrupt, or exhausted evidence
  creates a durable taint that blocks certified claims and completion until an
  explicit operator resolution starts a new evidence window.
- Runtime state is size-bounded, path-identity checked, atomically published,
  and protected by cross-process locks. Unknown or ambiguous state fails closed
  instead of being guessed away.

The result is intentionally narrower than formal program verification. Reconc
proves that the configured repository contract and the recorded current
evidence agree. It does not prove that the contract is complete, that the code
has no semantic defects, or that a hostile same-user process cannot replace
local state.

## Stack-aware assurance packs

Reconc ships 18 opt-in assurance packs. Detection can recommend a pack, but
Reconc never enables one silently, installs a toolchain, or invents a test,
lint, build, or typecheck command that the repository does not declare.

The shipped set is `go-assurance`, `rust-assurance`, `python-assurance`,
`java-assurance`, `csharp-assurance`, `cpp-assurance`, `php-assurance`,
`elixir-assurance`, `zig-assurance`, `shell-assurance`,
`powershell-assurance`, `typescript-assurance`, `nextjs-assurance`,
`svelte-assurance`, `npm-assurance`, `pnpm-assurance`, `yarn-assurance`, and
`bun-assurance`. The built-in `docs-sync` policy couples public behavior to
documentation, while the stack-neutral `agent` pack keeps context and
documentation evidence explicit.
[See every assurance contract](docs/documentation.md#policy-packs-and-native-assurance).

Packs are policy, not package installers. They remain inert until selected,
reuse the repository's own declared commands, and never install or invoke a
toolchain by themselves.

| Pack family | Representative controls |
| --- | --- |
| Go | Canonical formatting, owned goroutine patterns, tests, Vet, source hygiene, and configured process/network boundaries. |
| Rust, Python, Java, C#, C/C++, PHP, Elixir, Zig, Shell, PowerShell | Native test, format, analyzer, or build evidence plus changed-source hygiene appropriate to the stack. |
| TypeScript, Next.js, Svelte | Declared typecheck, framework build, route generation, lint, and changed-source hygiene without inventing package scripts. |
| npm, pnpm, Yarn, Bun | Exact dependency pins and current evidence for matching non-empty scripts that actually exist at the package boundary. |
| Stack-neutral `agent` and `docs-sync` | Required context reads and explicit coupling between public behavior and repository-owned documentation. |

Native assurance reads are bounded and fail closed on unreadable or oversized
authority surfaces. Network and process checks are deterministic source
heuristics with explicit path exemptions, not claims of full semantic analysis.
Repositories with different canonical commands can override or supplement the
bundled alternatives.

## Failure Modes Reconc Constrains

Reconc turns repository-visible agent failures into executable boundaries:

- **Premature completion:** `reconc done .` blocks until policy, repository
  state, current evidence, and typed TASK completion agree.
- **Scope drift and destructive edits:** configurable path rules stop writes or
  deletions outside the approved surface and protect generated files, secrets,
  docs, specs, architecture boundaries, and release assets.
- **Skipped or stale verification:** source changes can require fresh successful
  test evidence bound to the changed state, not an earlier green command.
- **Stubs presented as implementation:** source-hygiene gates catch leading
  `TODO`, `FIXME`, `STUB`, `PLACEHOLDER`, and language-specific unimplemented
  sentinels in changed shipped source.
- **Documentation drift:** `docs-sync` and repository-owned coupling rules can
  require public behavior changes to update the corresponding documentation.
- **Workflow bypass:** typed lifecycle gates preserve active work, evidence,
  transitions, and archives instead of accepting prose alone.
- **Stuck autonomous loops:** `reconc run on|off|reset|status|log` provides one
  durable repository switch with per-session no-progress release guards.
- **Cross-session context drift:** `reconc session-briefing . --json` combines
  current TASK state, policy delta, missing evidence, run state, and exact
  remediation into one bounded reentry contract.
- **Inconsistent agent behavior:** all supported adapters route into the same
  compiler and evaluator instead of maintaining agent-specific policy forks.
- **Evidence overflow or corruption:** segmented evidence and durable taint
  prevent incomplete history from being presented as a certified pass.
- **Stale policy repair deadlock:** the fail-closed hook path admits only a
  standalone `reconc refresh` repair invocation, never a
  compound command that smuggles unrelated work through the exemption.

Reconc does not make a model truthful and is not an operating-system sandbox.
Its [threat model and verified control map](docs/documentation.md#evidence-bound-completion-control)
state exactly what remains outside the boundary.

## Install and Bootstrap

Install the checksummed, provenance-attested v0.9.1 release once.

### Native release installation

macOS or Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.1/install.sh \
  | sh -s -- --version 0.9.1
export PATH="$HOME/.local/bin:$PATH"
reconc --version
```

Windows x64:

```powershell
$installer = Join-Path $env:TEMP "reconc-install.ps1"
Invoke-WebRequest https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.1/install.ps1 -OutFile $installer
& $installer -Version 0.9.1
Remove-Item $installer
$env:Path = "$env:LOCALAPPDATA\Programs\Reconc\bin;$env:Path"
reconc --version
```

The immutable `reconc-v0.9.1` tag contains both installer scripts, so these
commands do not fetch executable installation logic from mutable `main`. The
installers:

- resolve one explicit version or release channel;
- download bounded metadata and the exact platform artifact over HTTPS;
- require one matching SHA-256 entry and verify the payload before execution;
- inspect the candidate's embedded version, target, and source identity;
- verify GitHub build provenance when `gh` is available, or require it when
  `RECONC_REQUIRE_ATTESTATION=1`;
- smoke-test the candidate before replacing an existing binary;
- publish the binary and checksum-bound installation receipt under one lock;
- retain or restore the previous installation on failure.

They never edit shell configuration silently and never claim successful global
ownership until bare `reconc` resolves to the installed binary. POSIX defaults
to `~/.local/bin`; Windows defaults to
`%LOCALAPPDATA%\Programs\Reconc\bin`. If another binary shadows the target, the
installer prints the exact PATH remediation and stops short of a false healthy
receipt.

Windows releases currently support x64. Generated shell hook wrappers plus
`.sh` and extensionless policy scripts require `sh` on `PATH`; Git for Windows
supplies it. Native `.exe` and `.com` policy scripts execute directly.

### Source or portable installation

For a source build or copied binary:

```bash
go build -o .build/bin/reconc ./cmd/reconc
.build/bin/reconc install-cli
reconc --version
```

`install-cli` atomically installs the exact running executable, rejects a
symlink target, verifies checksum and executable mode, proves bare-command PATH
identity, and then publishes a private source-ownership receipt. A successful
repository bootstrap therefore never leaves operators navigating versioned
artifact paths. Use `reconc doctor --global` for independent read-only
diagnosis of owner, channel, running and resolved binaries, shadows, checksum,
and provenance.

The shipped CLI has no Bun, Node, Python, Docker, or service dependency. Bun
`1.3.14` is required only by contributors running the executable OpenCode and
Kilo adapter contract tests.

### Initialize a repository

Initialize the target repository:

```bash
cd /path/to/repository
reconc init .
```

`reconc init .` is the canonical transactional onboarding command. It detects
the safe profile and hooks, creates only absent files, records portable
ownership, and never overwrites drift. Mature or ambiguous repositories receive
one exact explicit-profile command instead of a guess.

Init performs one deterministic lifecycle: inspect, select, plan, apply,
compile when required, publish private rollback state and the committable
`.reconc/install.lock.json` ownership receipt, then verify. A fresh repository
defaults to the bounded `minimal` profile. Existing partial or mature control
state without a valid receipt receives no repository write until the profile is
explicit.

Drift is reviewable, not overwritten. Reconc creates hash-addressed candidate
files, and marker-only updates require explicit checksum-bound acceptance.
Policy, documentation, TASK files, and unrelated user bytes remain outside
ownership unless the selected profile explicitly creates them.

### Daily workflow

Use the same bare command from any repository:

```bash
reconc session-briefing . --json
reconc check . --write path/to/file
reconc next .
reconc done .
```

`session-briefing` is the bounded machine handshake for session entry and
reentry. `check` evaluates current evidence. `next` returns the highest-priority
remediation from the latest still-current block. `done` is the final
evidence-complete gate. Detailed static help stays on demand through
`reconc agent-intro --section NAME`, `reconc help <command>`, and
`reconc <command> --help`.

Inspection and enforcement commands never compile policy implicitly. If a
policy source changes, run `reconc refresh .` as its own explicit command,
review the lockfile diff, and commit source and lock together.

### Update the CLI and repository separately

Update Reconc itself with one command:

```bash
reconc update
```

`reconc update` checks ownership and release trust, installs an available stable
release atomically, and succeeds without changing anything when already
current. Equal version text is current only when the installed receipt's
artifact SHA-256 matches the selected release asset. Use `--channel preview`
or `--version VERSION` only for an intentional
selection. Source-owned installations receive the exact rebuild guidance.
There is no separate check/apply step in the current user flow. Direct updates
require GitHub build-provenance verification before publication. Offline
`--from-dir` updates additionally require the asset's Sigstore bundle and the
trusted-root file alongside the strict release inventory.

Global binary updates and repository-owned updates are separate. After a binary
update, review and apply repository changes explicitly:

```bash
reconc repo sync plan . --output /tmp/reconc-sync.json
reconc repo sync apply --plan /tmp/reconc-sync.json --digest <plan-digest>
reconc repo sync verify .
```

The update transaction changes only a verified receipt-owned direct
installation. Source-owned, unowned, ambiguous, shadowed, read-only, and
unsupported installations receive non-mutating remediation instead of an
unsafe replacement.

Repository sync is separately plan-, receipt-, precondition-, and digest-bound.
Planning is repository-read-only unless `--output` is supplied: its Git
snapshot uses an ephemeral object database, ignores ambient `GIT_*` routing,
and does not create Git objects or persistent Reconc state. Text output shows
current-to-target policy and harness pack identities, actionable changes,
blockers, an unchanged count, and exactly one next command; JSON retains the
complete deterministic plan.

Apply revalidates the Git snapshot, current owned bytes, managed blocks,
embedded packs, generated policy, binary identity, and plan digest under the
shared repository lock. The generated policy lock is compiled in memory and is
published only inside the transaction. Every mutation, including the portable
receipt, is recorded in a strict fsynced journal before the first target write.

Resolve a blocking action explicitly against the same saved plan and digest:

```bash
reconc repo sync resolve --plan /tmp/reconc-sync.json --digest <plan-digest> \
  --path path/to/artifact --strategy keep-current
reconc repo sync resolve --plan /tmp/reconc-sync.json --digest <plan-digest> \
  --path path/to/artifact --strategy use-target
```

`keep-current` preserves the current bytes and releases Reconc ownership;
`use-target` adopts the exact embedded target bytes. A cross-platform
receipt-owned binary requires `use-binary` plus an explicit local artifact,
SHA-256 checksum, and `OS/ARCH`. Invalid generated policy cannot be retained
with `keep-current`.

If apply or resolution is interrupted, every normal sync command fails closed
until recovery completes:

```bash
reconc repo sync recover .
```

Recovery finalizes an exact, fully verified after-image or restores recorded
before-images. A path that is neither image is reported as `refused` and is
never overwritten. Recovery conservatively leaves newly created empty parent
directories because their identity cannot be proven after a process crash.

### Autonomous TASK execution and final proof

For autonomous TASK execution, the agent operates the repository-scoped switch:

```bash
reconc run on
reconc run status
reconc run off
```

The durable switch verifies policy and executable TASK state before enabling,
continues supported agents through Stop, and releases no-progress sessions
without silently disabling repository mode. Final completion and portable
review remain explicit:

```bash
reconc exec . --staged -- go test ./...
reconc ci . --staged
reconc done .
reconc proof . --output proof.json
```

`reconc exec . --staged -- COMMAND` records the real exit status and publishes
a bounded proof only when the exact HEAD, index, and worktree candidate remains
unchanged. `reconc ci . --staged` accepts that candidate-bound evidence instead
of mutable session history.

`reconc done .` binds policy, Git state, active-session evidence, saved report
integrity, unresolved blocks, current staged proofs, and typed TASK completion
into a versioned report. `reconc proof .` renders the same candidate as
deterministic JSON or Markdown. A blocked candidate still produces a valid
bundle and exits 2; the exporter never runs missing tests or turns absent
evidence into a pass.

Portable proofs exclude absolute paths, user and home identity, session IDs,
prompts, transcripts, environment data, and raw command arguments. Command
receipts expose a redacted executable summary and a SHA-256 command identity.

The [complete guide](docs/documentation.md), [command reference](docs/commands.md),
and built-in `reconc <command> --help` cover exact flags, profiles, trust,
rollback, platform limitations, and recovery paths.

## Rollout Modes

Profiles define ownership, not quality tiers:

| Profile | Intended repository | Owned surface |
| --- | --- | --- |
| `minimal` | Fresh repository that needs the smallest safe control plane | Policy, managed agent-orientation block, runtime ignores, compiled lock, and detected hooks. |
| `governed` | Repository adopting Reconc TASK and documentation governance | Minimal plus typed TASK state, documentation, `start.md`, and stable hook wrapper. |
| `advanced` | Repository that needs the complete portable harness | Governed plus the immutable embedded `advanced@1.0.0` harness pack and its manifest digest. |
| `existing` | Mature repository that already owns policy, instructions, docs, and TASK state | Only explicitly selected hooks, wrapper, and optional stable binary; existing control-plane files remain user-owned. |

Detection may recommend a profile, pack, or hook, but Reconc never silently
enables an assurance pack or invents a repository command. Explicit selections
are recorded in the plan and portable receipt so later sync and removal operate
within the same ownership boundary.

Minimal policy and hook bootstrap:

```bash
reconc --version
reconc init .
```

Explicit full repo-local governance rollout:

```bash
reconc init . --profile governed --hook codex --json
```

Complete embedded public harness rollout:

```bash
reconc init . --profile advanced --no-hooks --json
```

Init atomically installs the exact running build as the stable user CLI, then
refuses before repository writes unless bare `reconc` resolves to it. It
performs inspect, selection, plan, apply, receipt publication, and verification
through the same bootstrap engine. `bootstrap inspect|profiles|plan|apply|
verify|remove` remains the transparent lower-level interface for operators who
need a separately reviewed plan. Packs and hooks are explicit when supplied;
safe fresh-repository hook detection is deterministic. Apply publishes absent
targets, leaves exact files unchanged, creates hash-addressed candidates for
drift, and rolls back transaction-owned files on failure.

Remove only portable-receipt-owned bootstrap artifacts or one selected
platform hook:

```bash
reconc bootstrap remove --plan <plan-path-from-init>
reconc hook uninstall codex .
```

Removal verifies hashes, removes exact owned files and generated artifacts,
strips only exact marker-owned blocks, refuses drift, and emits review
candidates instead of deleting ambiguous user content. An older private
receipt cannot expand ownership beyond `.reconc/install.lock.json`; user-owned
policy, docs, TASKs, and bytes outside managed markers remain.

Mature repositories that already own policy, agent instructions, docs, and
TASK state use `--profile existing` after `reconc refresh .`. That profile
requires a fresh lockfile and owns only explicitly selected hooks, the
repo-local wrapper, and an optional stable binary. It rejects `--pack` and
leaves every existing control-plane file untouched.

Advanced project-harness rollout:

1. Install Reconc once and verify bare `reconc`.
2. Run `reconc init . --profile advanced --no-hooks --json`.
3. Verify the receipt reports `advanced@1.0.0` and its pack digest.
4. Have an agent read
   `tools/reconc/harness/template/BOOTSTRAP.md` in the target repository.
5. Use its manual path only for project naming, stack, architecture, merge,
   and verification surfaces beyond the universal governed profile.

The binary embeds the exact checksummed pack also published as
`reconc-harness-pack-advanced-1.0.0.zip`; no source checkout, mutable download,
or arbitrary directory copy participates in init. The versioned guide remains
the AI recovery tutorial and parity checklist when a transaction reports drift
or a mature repository needs surgical adaptation.

## Architecture and Operational Boundaries

Reconc is one Go CLI with no daemon. The executable keeps command parsing thin
and routes behavior into internal packages with explicit boundaries:

| Layer | Responsibility |
| --- | --- |
| Ingest, parser, compiler | Discover repository roots and policy sources, validate strict YAML and templates, resolve packs, detect conflicts, generate portable canonical lockfiles, and migrate supported lock formats. |
| Runtime and assurance | Evaluate normalized evidence, path and command rules, native repository assurance, scripts, templates, remediation, and Git-derived candidates. |
| Hooks and agent sessions | Generate platform artifacts, normalize untrusted host payloads, enforce pre-action policy, record bounded outcomes, manage compaction context, and evaluate Stop. |
| Bootstrap and harness packs | Inspect repositories, build deterministic plans, publish create-only artifacts and ownership receipts, embed the advanced pack, synchronize owned state, and roll back failed transactions. |
| Global CLI lifecycle | Install, diagnose, update, and uninstall one bare `reconc` command using checksum-bound ownership and cross-process locks. |
| TASK and completion | Parse typed TASK profiles, publish recoverable transitions, build final completion reports, retain unresolved decisions, and export portable proof bundles. |
| Audit and retention | Maintain a SHA-256-linked, sequence-checked decision ring with a detached chain head, plus evidence segments, liveness records, command proofs, reports, and owned temporary-state cleanup. |
| Release trust | Build deterministic multi-platform binaries, strict manifests, checksums, manpages, completions, SBOMs, and provenance-bound artifacts. |

The state model keeps global installation, repository ownership, compiled
policy, and mutable runtime evidence separate:

| State | Location or identity | Commit? | Purpose |
| --- | --- | --- | --- |
| Global installation receipt | `$RECONC_HOME/install/receipt.json` | No | Identifies the installed binary, owner, version, checksum, channel, target, and provenance. |
| Repository installation receipt | `.reconc/install.lock.json` | Yes | Defines portable ownership for bootstrap, sync, verification, and bounded removal. |
| Compiled policy | `.reconc/policy.lock.json` | Yes | Portable deterministic contract reviewed with policy-source changes. |
| Runtime evidence | Bounded repository and user state under Reconc-owned roots | No | Session events, reports, unresolved blocks, run decisions, staged proofs, liveness, and retention metadata. |
| Repository run state | `.reconc/run/` | No | Durable repository-scoped autonomous switch and bounded decision log. |
| TASK control plane | Repository-configured Markdown overview and detail paths | Yes | Human-readable and machine-validated work state. |

This separation prevents a binary update from silently rewriting repository
policy, a repository sync from claiming user-owned files, or runtime evidence
from becoming a committable project artifact. Each mutating lifecycle has its
own lock, ownership check, atomic publication path, and rollback boundary.

Public JSON contracts carry explicit `format_version` and schema identities.
Canonical ordering, normalized paths, portable root identity, and self-digests
make equivalent inputs reviewable across clones and worktrees. Failures in
write, sync, close, unlock, or final verification are propagated instead of
being reported as successful partial publication.

## Supported Agent Runtimes

| Runtime | Integration |
| --- | --- |
| Claude Code | repo-local hook wiring |
| Codex | session, tool, permission, evidence, and Stop hooks with `apply_patch` path extraction |
| GitHub Copilot | contract-tested repository hooks for Copilot CLI and coding agent; hard PreToolUse and Stop decisions where the host fires them; host timeouts remain fail-open |
| Cursor | one registry-generated `.cursor/hooks.json` for Agent/Cmd+K, Tab, CLI, and supported cloud routes; registry-derived `surface_events`, native prompt/subagent decisions, sessionless workspace liveness, and tool/Stop behavior remain event-specific |
| OpenCode | thin project plugin with strict `metadata.exit` shell outcomes, permission/tool/session/compaction routes, and bounded asynchronous `session.idle` continuation |
| Devin CLI | native session, prompt, tool, permission, stop, and post-compaction hooks |
| Antigravity CLI | invocation, tool, post-tool, and stop hook coverage |
| Kilo Code | thin CLI/VS Code project plugin with strict `metadata.exit` shell outcomes and the same bounded asynchronous continuation contract as OpenCode |
| Grok Build | native lifecycle and hard PreToolUse hooks, strict ACP continuation, and leader-mode TUI steering |
| Kimi Code CLI | explicit user-global `$KIMI_CODE_HOME/config.toml` integration for all 16 native hook events; repository discovery prevents global hooks from acting outside initialized Reconc repositories |

Integration claims use precise states:

| State | Meaning |
| --- | --- |
| `configured` | The generated project artifact is present, current, executable where required, and locally activatable. |
| `discoverable` | The host contract documents and scans the configured artifact path. |
| `loaded` | A current host process emitted a lifecycle route attributable to that artifact. |
| `observed` | The exact named route produced current bounded liveness evidence. |
| `enforced` | A disposable negative probe proved the pre-action route blocked before the side effect. |
| `inferred` | Reconc maps a weaker host lifecycle to a capability, such as `session.idle` to continuation. |
| `degraded` | A required artifact, activation, identity, route, API, or proof is missing. |
| `unsupported` | The host does not expose the required lifecycle on that surface, or Reconc has no sound implementation for it. |

Claude Code, Codex, GitHub Copilot, Cursor, Devin CLI, Antigravity CLI, and
Kimi Code CLI expose a synchronous Stop event. GitHub Copilot host timeouts
remain fail-open,
and this adapter is contract-tested rather than claimed live until
`reconc hook status . --json` records real events. OpenCode and Kilo Code
expose `session.idle`; Reconc submits at most one bounded asynchronous
continuation per new activity generation, but that inferred fail-open adapter
is not an equivalent host-level Stop gate. Cursor's shared project file does
not imply event parity between Agent, Cmd+K, Tab, interactive CLI, print CLI,
and cloud agents. Static `configured` state and per-route `observed` liveness
remain separate. The exact support-state and event matrix is in
[the platform contract](docs/documentation.md#host-integration-truth). All
platforms still use git pre-commit as the hard repository backstop.

Cursor CLI's primary executable is `agent`; `cursor-agent` is its
backward-compatible alias. Reconc validates the executable identity instead of
trusting either command name. Current official CLI documentation establishes
`sessionStart`, `sessionEnd`, `beforeSubmitPrompt`, `preToolUse`,
`postToolUse`, `stop`, and sessionless `workspaceOpen`; the same installed
artifact can receive additional routes, but Reconc leaves them surface-
unproven until exact liveness exists. Cursor currently omits generic tool hooks
for `AskQuestion`, so that host action has no Reconc pre-action boundary.

Kimi Code hooks are user-global rather than repository-local. Reconc never
selects them during `init` or bootstrap. An operator explicitly runs
`reconc hook install kimi-code`; Reconc merges one marker-owned block into
`$KIMI_CODE_HOME/config.toml` (default `~/.kimi-code/config.toml`) while
preserving unrelated TOML, and `reconc hook uninstall kimi-code` removes only
that exact block. Each global invocation silently no-ops unless its working
directory discovers an explicit Reconc repository. Kimi treats exit code 2 as
the blocking contract for `PreToolUse`, `UserPromptSubmit`, and `Stop`, while
hook crashes, other non-zero exits, and host timeouts remain fail-open. Static
configuration and isolated contract tests do not claim a live Kimi execution.

Use the following commands before claiming a runtime is active:

```bash
reconc hook status . --json
reconc doctor . --deep
```

`hook status` keeps static `configured` truth and registry-derived
`surface_events` separate from
`expected_events`, `live_events`, `unseen_events`, `last_seen`, and
`last_event`. A generated file or passing adapter contract test is not
represented as live execution proof.

Host payloads are untrusted and capability-specific. Reconc caps input,
process output, timeouts, evidence collections, and stored diagnostics. It
accepts positive command evidence only from authoritative host outcome fields,
never from stdout text. Cursor, OpenCode, and Kilo MCP mappings use exact
platform, server fingerprint, tool, and JSON Pointer selectors; malformed or
unclassified calls produce no positive repository evidence.

## Release Trust and Reproducibility

Every release is built from an existing protected `reconc-vX.Y.Z` tag through
an explicit manual workflow dispatch. Tag pushes alone do not publish.
Branch-ref dispatches are rejected, so build provenance binds to the release
tag rather than a moving branch.

The release inventory includes:

- native macOS arm64/amd64, Linux arm64/amd64, and Windows amd64 binaries;
- immutable POSIX and PowerShell installers;
- Bash, Zsh, and Fish completion artifacts plus a generated manpage;
- the embedded advanced harness pack as a standalone checksummed archive;
- public v1 schemas, the legacy v2 policy-lock schema, and the current v3
  policy-lock schema;
- deterministic SPDX 2.3 and CycloneDX 1.6 SBOMs;
- strict `release-manifest.json` and `SHA256SUMS`.

Release verification rejects missing, extra, duplicate, unsafe, stale,
noncanonical, mutable, or corrupted artifacts. Generated completions, manpage,
schemas, installers, harness archive, manifest entries, checksums, SBOMs, and
embedded binary provenance are regenerated or byte-compared before upload.
GitHub build-provenance attestations bind each manifest-listed artifact to the
tagged workflow run.

Binaries use a pinned Go toolchain, `CGO_ENABLED=0`, and `-trimpath`.
`SOURCE_DATE_EPOCH` supplies deterministic timestamps for SBOM and manpage
generation. This supports reproducible output from the same toolchain and
source, but the repository does not claim an independent third-party rebuild
attestation.

## OpenAI Build Week

Reconc is an existing project that was meaningfully extended during OpenAI
Build Week. The pre-event boundary is commit
[`2daa537`](https://github.com/Christopher-Schulze/reconc/commit/2daa5372b08d7f479d895b2b5419a39026eb6719),
committed on June 8, 2026. Only work after the official July 13 start is part
of the Build Week submission.

During that window, Codex with GPT-5.6 was used substantively to design,
implement, test, and harden the portable compiler, evidence-complete `done`
gate, typed TASK lifecycle, repository run control, transactional bootstrap,
runtime hooks, cross-platform behavior, portable proof flow, and release trust.
Christopher Schulze set the product direction and made the final decisions.
Claude also assisted with portions of implementation and review; public commit
trailers preserve that attribution instead of presenting every change as Codex
work.

The immutable [`v0.8.6` release](https://github.com/Christopher-Schulze/reconc/releases/tag/reconc-v0.8.6)
contains the judge-ready binaries. The Build Week implementation extends the
June 8 baseline with the evidence-complete `done` gate, portable proof export,
transactional bootstrap UX, repository run-loop controls, truthful runtime
adapters, generic npm/pnpm/Yarn/TypeScript assurance, and the rebuilt public
product surface. Core policy compilation, evaluation, hook processing, run
control, and proof generation remain offline at runtime. Supported hosts own
their model authentication and inference traffic. Codex and GPT-5.6 helped
build Reconc but are not dependencies of it.

## Production dogfooding

Reconc is dogfooded in a large private codebase for an agentic enterprise
platform being built for the author's startup. Real agent failures there drive
generic controls in this standalone open-source product. The private repository
is not published, and this repository does not claim byte-identical parity or
expose private architecture, source, prompts, or task details.

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

The second command shows the missing evidence. The third command supplies a
current test result and should pass unless another rule blocks it. Hook sessions
track write epochs, so a successful command recorded before a later matching
write no longer satisfies `require_command_success`.

Exit codes are stable for humans, agents, and CI:

- `0`: pass, warn, or informational success
- `1`: runtime or input error
- `2`: blocking policy violation

## Agent Skill

The repo ships an agent-facing skill at `skills/reconc/SKILL.md`.

Use it as the reconc operating guide for Codex, OpenCode, Claude Code, and
other coding agents. The skill gives every agent the same operating loop:

- check policy health before work
- begin and reenter with `reconc session-briefing . --json`
- collect truthful read, write, command, and claim evidence
- remediate blocks with `reconc next .`
- run `reconc done .` before claiming completion
- distinguish native hook enforcement from CLI self-checks
- operate `reconc run on|off|reset|status` itself when autonomous TASK execution or state recovery is requested

Detailed runtime behavior lives in `docs/documentation.md` and
`docs/architecture.md`.

## Policy Files

Commit:

- `.reconc.yml` for repo policy configuration
- `.reconc/install.lock.json` for portable repository ownership and sync identity
- `.reconc/policy.lock.json` for the portable compiled policy contract
- `AGENTS.md` for agent-facing project instructions
- `skills/reconc/SKILL.md` for portable agent usage

Do not commit mutable runtime state:

- `.reconc/audit.jsonl*`
- `.reconc/cache/`
- `.reconc/locks/`
- `.reconc/sessions/`
- `.reconc/reports/`
- `.reconc/run/`
- `.reconc/task-transaction.json`
- `.reconc/repository-sync-transaction.json`
- `.reconc/bootstrap-*.json`
- `*.reconc-candidate-*`
- `*.reconc-remove-candidate-*`
- `dist/`
- `tools/reconc/dist/`

## FAQ

### Is Reconc another coding agent?

No. Reconc does not generate code or call a model. It is the deterministic
control and evidence layer around whichever coding agent you already use.

### Does Reconc solve reward hacking?

No. Reconc does not infer intent or make an arbitrary model honest. It blocks
or detects configured, repository-visible failure modes such as unsupported
completion claims, stale test evidence, protected writes, unfinished TASKs,
and no-progress loops. Uninstrumented hosts, external systems, semantic defects
outside configured checks, and hostile same-user processes remain outside its
guarantee.

### Is it a security sandbox?

No. Reconc fails closed at repository, hook, Git, CI, and completion boundaries,
but a hostile same-user process can still replace local files or bypass local
hooks. Use an external sandbox and protected remote CI for adversarial code.

### Does it work offline?

Yes for core repository control. The shipped Go binary makes no network calls
for policy compilation, evaluation, hooks, run control, repository sync, or
proof generation.
Installation, direct updates, and GitHub publication naturally require network
access. Uninstall and core repository control remain offline.

### Which agents are supported?

Claude Code, Codex, GitHub Copilot, Cursor, OpenCode, Devin CLI, Antigravity
CLI, Kilo Code, Grok Build, and Kimi Code CLI have registry-backed
integrations. Their host capabilities are not identical; `reconc hook status
. --json` reports
the static activation state separately from per-route live evidence without
inflating the claim.

### Why not use only pre-commit hooks and CI?

Keep both. Git hooks and CI are important enforcement boundaries, but neither
normally tracks causal session evidence, typed TASK state, current
agent-runtime behavior, unresolved local policy blocks, or whether a successful
command still matches the final candidate. Reconc unifies those signals and
keeps git pre-commit plus remote CI as backstops.

### Does Reconc run my missing tests automatically?

No. Reconc does not invent or silently run a missing test, lint, build, or
typecheck command. It records real outcomes supplied by supported runtimes or
explicit `reconc exec`, and it can execute only explicitly policy-authored
`require_script` gates during evaluation. Missing evidence remains a block with
one exact remediation.

### What exactly does `reconc done .` prove?

It proves that the configured current policy, captured Git candidate, accepted
current evidence, saved report integrity, unresolved-decision state, staged
proofs, and typed TASK completion agree. It does not prove that the policy is
complete or that the program is semantically correct for every possible input.

### Can I share a Reconc result with a reviewer?

Yes. `reconc proof . --format markdown` produces a human-readable bundle, and
the default JSON form is suitable for automation. Both derive from the same
typed completion state, include a self-digest, and exclude private prompts,
transcripts, session IDs, environment values, absolute home paths, and raw
command arguments.

### Does Reconc manage project scope or priorities?

No. Reconc validates and safely mutates the configured TASK grammar, but
humans and project agents still own scope, acceptance criteria, priority, and
evidence quality. The control plane prevents malformed transitions and
unfinished completion from being accepted; it does not choose the product
roadmap.

### What happens when evidence is incomplete or corrupted?

Reconc fails closed. Recoverable capacity pressure seals evidence into linked
segments. Missing or corrupt segments, an event too large for an empty segment,
or exhausted segment capacity creates durable evidence taint. Read-only
diagnosis remains available, but certified claims and completion remain blocked
until the active session ends and an operator explicitly resolves the taint
with the token printed by `reconc hook evidence-status .`.

### How do I upgrade, troubleshoot, or remove it?

Use `reconc update` for the global CLI. It checks and applies an available
verified update in one transaction or succeeds without mutation when already
current. Repository-owned hooks and harness artifacts are updated separately
through a reviewed `reconc repo sync` plan.

See the canonical [FAQ](docs/documentation.md#faq),
[Troubleshooting](docs/documentation.md#troubleshooting),
[Upgrading](docs/documentation.md#upgrading), and
[Uninstall/Remove](docs/documentation.md#uninstall-and-remove) guides.

## Deeper Documentation

Current product documentation lives in `docs/documentation.md`. That file is
the source of truth for installation, workflow, architecture, release, security,
and git-ignore policy.

- `docs/architecture.md` covers contributor internals and the hook-runtime
  threat model.
- `docs/commands.md` is the full command reference; `reconc <command> --help`
  remains the exact flag reference.
- `docs/rfcs/` contains frozen contracts for the lockfile, reports, rule
  kinds, presets, templates, and hooks.
- local source-planning files such as `docs/tasks.md`, `docs/tasks/`,
  `docs/todo.md`, `docs/todo/`, and `CHANGELOG.md` are ignored and are not part
  of the published repo state.

This source-repository ignore policy does not change the product contract:
governed target repositories still receive and commit their own TASK control
plane, while Reconc's own implementation queue remains local.

Security policy lives in `SECURITY.md`.

## Security boundary

Reconc is a deterministic repository control plane, not an operating-system
sandbox. A deliberately hostile same-user process can replace local policy,
hooks, state, or binaries, fabricate self-reported evidence, or bypass a Git
hook. Strong adversarial enforcement therefore requires an external sandbox
and protected remote CI or branch rules outside the agent's write authority.

The trust model is explicit:

| Surface | Reconc behavior | Residual risk |
| --- | --- | --- |
| Policy and lockfiles | Strict schemas, source digests, lock digests, portable roots, and stale-state failure. | A trusted policy owner can still omit a real requirement. |
| Hook payloads | Bounded parsing, filesystem identity checks, exact outcome handling, and fail-closed decisions where the host supports them. | Host timeouts, unsupported events, and host-owned fail-open behavior remain outside Reconc. |
| Repository files | Scope rules, candidate identity, receipt-bounded mutation, atomic writes, and drift refusal. | A hostile same-user process can bypass hooks or replace local bytes. |
| Command evidence | Causal epochs, exact staged candidate binding, bounded receipts, and exit-status validation. | A malicious trusted command or compromised toolchain can still produce deceptive output. |
| Portable proof | Deterministic typed output, current candidate identity, redaction, and self-digest. | A bundle proves the configured contract, not universal correctness or independent remote execution. |
| Release artifacts | Checksums, strict manifest, embedded provenance, SBOMs, tagged workflow attestation, and installer verification. | No independent third-party reproducible-build attestation is claimed. |

Repository-local development binaries and hook wrappers are writable by the
repository owner and are not re-attested on every hook event. That is an
availability and developer-convenience contract inside the documented
non-hostile same-user model. Put the repository inside a stronger sandbox when
the agent or another same-user process must be treated as adversarial.

Security reports belong on the private route in [SECURITY.md](SECURITY.md).
Include the command, policy shape, lockfile state, host/runtime, payload
structure where relevant, and a minimal reproduction.

For command details:

```
reconc <command> --help
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, change scope,
verification requirements, and the pull-request checklist. Run `make test`,
`make coverage`, `make vet`, `make lint`, `make self-host`, and
`make publication-audit` before proposing a change. `make coverage` measures
the complete root and portable-template modules with cross-package
instrumentation and rejects regressions below their explicit floors;
`make cover` additionally writes separate HTML reports. Report vulnerabilities
through the private route in
[SECURITY.md](SECURITY.md), not a public issue.

## Status

The source line is `v0.9.x`, and the current source version is `v0.9.1`.
Release artifacts are produced only by
an explicit manual workflow dispatch that uses an existing
`reconc-vX.Y.Z` tag as both workflow ref and input; branch-ref dispatches are
rejected and tag pushes never publish a release. Every published release SBOM
is regenerated and byte-verified before its checksum and build provenance are
published.

Candidate CI runs full root and portable-template tests on Ubuntu and macOS,
native Windows tests and installer failure paths, whole-module coverage floors,
formatting, tidy checks, Vet, pinned Staticcheck, pinned Govulncheck, race
tests, release-trust tests, publication-boundary checks, harness-pack parity,
and Go CodeQL. GitHub Actions are allowlisted and commit-pinned, checkout
credentials are not persisted, and release/publication jobs use full history
where the post-boundary audit requires it.

`make self-host` builds the local binary and runs the clean-repository golden
path across all three bootstrap profiles, git pre-commit plus all ten agent
runtimes, TASK lifecycle, retention, and stable release-layout binary
resolution.

`make publication-audit` scans every tracked file present in the working tree
plus every commit after the documented legacy-history boundary for private
project vocabulary, personal absolute paths, session/share material,
secret-shaped values, sensitive filenames, and placeholder residue. CI and
release gates run it with full Git history; it does not rewrite or claim to
erase older public history.

The protected `main` ruleset rejects deletion, non-fast-forward updates, and
unchecked candidates. A pull request is not mandatory, but the same required
Ubuntu, macOS, Windows, release-trust, and CodeQL checks must succeed for the
exact commit before the branch can advance.

## License

MIT License. Copyright (c) 2026 Christopher Schulze.
