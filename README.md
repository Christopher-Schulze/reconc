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

[Demo](#see-the-real-loop-in-under-a-minute) · [Install](#install-and-bootstrap) · [Documentation](docs/documentation.md) · [Security](SECURITY.md) · [Contributing](CONTRIBUTING.md) · [Issues](https://github.com/Christopher-Schulze/reconc/issues/new/choose) · [Releases](https://github.com/Christopher-Schulze/reconc/releases)

An agent saying "done" is a claim. Reconc verifies that claim against the
current repository, policy, evidence, and TASK state. Missing tests, stale
proof, protected edits, documentation drift, unfinished work, and stuck loops
become deterministic decisions with one exact next action.

No second model judges the first. One local Go binary compiles repository-owned
rules and enforces them across the CLI, Git, CI, and nine coding-agent
integrations. The exact
[guarantees and limits](docs/documentation.md#evidence-bound-completion-control)
stay explicit.

## See the real loop in under a minute

With Reconc on `PATH`:

```bash
reconc demo
```

The demo creates an isolated Git repository and runs the real product path:
policy compilation, a blocked write, missing-proof remediation, a real test,
evidence-complete completion, and portable proof export. It uses no model or
network and cleans up by default.

```text
[BLOCK] out-of-scope write
[BLOCK] source change without current test proof
[REMEDIATE] one exact next action
[PASS] real test evidence bound to the changed state
[DONE] evidence-complete proof verified
[PROOF] portable proof bundle verified
```

Use `reconc demo --keep` to retain every artifact or `reconc demo --json` for
the machine-readable result. Installation takes one verified native script
and is documented below.

## How Reconc Works

**Repository rules** → **compiled policy lock** → **CLI, Git, CI, and agent
hooks** → **pass, warn, or block with one exact next action**

`reconc` does not make LLMs deterministic. It makes the boundary around their
work deterministic: which files are protected, which commands must have run,
which claims must be supplied, which hook events are allowed to continue, and
why a task is allowed to be called done.

## What Reconc Controls

| Control surface | What Reconc provides |
| --- | --- |
| Policy | Compiles `AGENTS.md`, `.reconc.yml`, presets, templates, and project files into a deterministic, portable lockfile. Invalid or stale policy fails closed. |
| Actions | Controls reads, writes, commands, claims, protected paths, coupled changes, and configured MCP effects at supported boundaries. |
| Evidence | Binds successful commands to the repository state they verified, so later relevant writes invalidate stale proof. |
| Completion | `reconc done .` accepts completion only when policy, Git state, evidence, unresolved blocks, staged proofs, and typed TASK state agree. `reconc proof .` exports the same candidate without private session data. |
| Autonomy | Typed TASK transitions, durable run control, bounded evidence, and no-progress guards keep long agent runs recoverable and finite. |
| Delivery | Transactional init, repository sync, global update, safe removal, checksummed releases, provenance attestations, and SBOMs keep ownership explicit. |
| Integrations | One registry-backed contract spans Claude Code, Codex, GitHub Copilot, Cursor, OpenCode, Devin CLI, Antigravity CLI, Kilo Code, Grok Build, and git pre-commit. |

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

Reconc does not make a model truthful and is not an operating-system sandbox.
Its [threat model and verified control map](docs/documentation.md#evidence-bound-completion-control)
state exactly what remains outside the boundary.

## Install and Bootstrap

Install the checksummed, provenance-attested v0.9.1 release once.

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

Both installers verify checksum, embedded identity, and provenance before
atomic publication. They never edit shell configuration silently and print the
exact PATH fix when needed. For a source build or copied binary:

```bash
go build -o .build/bin/reconc ./cmd/reconc
.build/bin/reconc install-cli
reconc --version
```

Test the real loop, then initialize a repository:

```bash
reconc demo
cd /path/to/repository
reconc init .
```

`reconc init .` is the canonical transactional onboarding command. It detects
the safe profile and hooks, creates only absent files, records portable
ownership, and never overwrites drift. Mature or ambiguous repositories receive
one exact explicit-profile command instead of a guess.

Use the same bare command from any repository:

```bash
reconc session-briefing . --json
reconc check . --write path/to/file
reconc next .
reconc done .
```

Update Reconc itself with one command:

```bash
reconc update
```

`reconc update` checks ownership and release trust, installs an available stable
release atomically, and succeeds without changing anything when already
current. Use `--channel preview` or `--version VERSION` only for an intentional
selection. Source-owned installations receive the exact rebuild guidance.
There is no separate check/apply step in the current user flow.

Global binary updates and repository-owned updates are separate. After a binary
update, review and apply repository changes explicitly:

```bash
reconc repo sync plan . --output /tmp/reconc-sync.json
reconc repo sync apply --plan /tmp/reconc-sync.json --digest <plan-digest>
reconc repo sync verify .
```

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

The [complete guide](docs/documentation.md), [command reference](docs/commands.md),
and built-in `reconc <command> --help` cover exact flags, profiles, trust,
rollback, platform limitations, and recovery paths.

## Rollout Modes

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

## Supported Agent Runtimes

| Runtime | Integration |
| --- | --- |
| Claude Code | repo-local hook wiring |
| Codex | session, tool, permission, evidence, and Stop hooks with `apply_patch` path extraction |
| GitHub Copilot | contract-tested repository hooks for Copilot CLI and coding agent; hard PreToolUse and Stop decisions where the host fires them; host timeouts remain fail-open |
| Cursor | one registry-generated `.cursor/hooks.json` for Agent/Cmd+K, Tab, CLI, and supported cloud routes; successful and failed tool outcomes, shell policy, exact MCP boundaries, subagents, compaction, and Stop remain event- and surface-specific |
| OpenCode | thin project plugin with strict `metadata.exit` shell outcomes, permission/tool/session/compaction routes, and bounded asynchronous `session.idle` continuation |
| Devin CLI | native session, prompt, tool, permission, stop, and post-compaction hooks |
| Antigravity CLI | invocation, tool, post-tool, and stop hook coverage |
| Kilo Code | thin CLI/VS Code project plugin with strict `metadata.exit` shell outcomes and the same bounded asynchronous continuation contract as OpenCode |
| Grok Build | native lifecycle and hard PreToolUse hooks, strict ACP continuation, and leader-mode TUI steering |

Claude Code, Codex, GitHub Copilot, Cursor, Devin CLI, and Antigravity CLI
expose a synchronous Stop event. GitHub Copilot host timeouts remain fail-open,
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

## OpenAI Build Week

Reconc is an existing project that was meaningfully extended during OpenAI
Build Week. The pre-event boundary is commit
[`2daa537`](https://github.com/Christopher-Schulze/reconc/commit/2daa5372b08d7f479d895b2b5419a39026eb6719),
committed on June 8, 2026. Only work after the official July 13 start is part
of the Build Week submission.

During that window, Codex with GPT-5.6 was used substantively to design,
implement, test, and harden the portable compiler, evidence-complete `done`
gate, typed TASK lifecycle, repository run control, transactional bootstrap,
runtime hooks, cross-platform behavior, deterministic demo, and release trust.
Christopher Schulze set the product direction and made the final decisions.
Claude also assisted with portions of implementation and review; public commit
trailers preserve that attribution instead of presenting every change as Codex
work.

The immutable [`v0.8.6` release](https://github.com/Christopher-Schulze/reconc/releases/tag/reconc-v0.8.6)
contains the judge-ready binaries. The Build Week implementation extends the
June 8 baseline with the evidence-complete `done` gate, deterministic demo,
transactional bootstrap UX, repository run-loop controls, truthful runtime
adapters, generic npm/pnpm/Yarn/TypeScript assurance, and the rebuilt public
product surface. Core policy compilation, evaluation, hook processing, run
control, and proof generation remain offline at runtime. The explicit
`reconc grok` command launches the operator-installed Grok ACP process, whose
authentication and model traffic are owned by Grok. Codex and GPT-5.6 helped
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
The explicit `reconc grok` command launches the external Grok ACP process and
therefore depends on Grok's authentication and model service. Installation,
direct updates, and GitHub publication naturally require network access.
Uninstall and core repository control remain offline.

### Which agents are supported?

Claude Code, Codex, GitHub Copilot, Cursor, OpenCode, Devin CLI, Antigravity
CLI, Kilo Code, and Grok Build have registry-backed integrations. Their host
capabilities are not identical; `reconc hook status . --json` reports
installed, configured, live, degraded, or unsupported state without inflating
the claim.

### How do I upgrade, troubleshoot, or remove it?

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

`make self-host` builds the local binary and runs the clean-repository golden
path across all three bootstrap profiles, git pre-commit plus all nine agent
runtimes, TASK lifecycle, retention, and stable release-layout binary
resolution.

`make publication-audit` scans every tracked file plus every commit after the
documented legacy-history boundary for private project vocabulary, personal
absolute paths, session/share material, secret-shaped values, sensitive
filenames, and placeholder residue. CI and release gates run it with full Git
history; it does not rewrite or claim to erase older public history.

## License

MIT License. Copyright (c) 2026 Christopher Schulze.
