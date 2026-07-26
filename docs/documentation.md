# reconc: Repository Control Compiler Documentation

This file is the source of truth for current reconc product documentation.
RFCs may remain in `docs/` as frozen contracts, but user-facing installation,
usage, architecture, release, and security facts should be kept here first.

## Contents

- [Product](#product)
- [Evidence-Bound Completion Control](#evidence-bound-completion-control)
- [Install And Build](#install-and-build)
- [Transactional Bootstrap](#transactional-bootstrap)
- [Daily Workflow](#daily-workflow)
- [FAQ](#faq)
- [Troubleshooting](#troubleshooting)
- [Upgrading](#upgrading)
- [Uninstall And Remove](#uninstall-and-remove)
- [Development Control Plane](#development-control-plane)
- [Minimal Example Policy](#minimal-example-policy)
- [Command Surface](#command-surface)
- [Repository Policy](#repository-policy)
- [Policy Packs And Native Assurance](#policy-packs-and-native-assurance)
- [Architecture](#architecture)
- [Agent Skill](#agent-skill)
- [Publication Boundary](#publication-boundary)
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

The controlled failure modes include premature completion, out-of-scope or
protected writes and deletions, missing reads, missing or stale verification,
unsupported claims, shipped-code implementation-debt markers and unimplemented
sentinels, documentation drift, invalid TASK transitions, and repeated
no-progress continuation. These controls remain repository-bounded and do not
replace an operating-system sandbox.

The product is a standalone Go CLI. It does not require Docker, Node, Python,
or a local service. Runtime behavior should stay offline by default.

Human-readable output colors decisions, rule IDs, and `OK`/`WARN`/`FAIL` tags
only on an interactive terminal. `NO_COLOR`, `TERM=dumb`, JSON, files, and
pipes always remain plain.
The dependency-free `tui` view is always plain text and bounds each line to a
valid `COLUMNS` value, while JSON remains width-independent.

## Evidence-Bound Completion Control

The primary product category is **evidence-bound completion control for AI
coding agents**. The supporting thesis is narrower than a general AI-safety
claim: Reconc provides **repository-bounded resistance to concrete reward-
hacking and specification-gaming failure modes** when a coding agent's claim
can be checked against configured repository state and current evidence.

The terminology is grounded in distinct source concepts:

- [Amodei et al., *Concrete Problems in AI Safety*](https://arxiv.org/abs/1606.06565)
  treats reward hacking as an objective-function safety problem: an agent can
  obtain reward without producing the intended result.
- [Krakovna et al., *Specification gaming: the flip side of AI ingenuity*](https://deepmind.google/blog/specification-gaming-the-flip-side-of-ai-ingenuity/)
  defines specification gaming as satisfying the literal objective without
  achieving the intended outcome.
- [Manheim and Garrabrant, *Categorizing Variants of Goodhart's Law*](https://arxiv.org/abs/1803.04585)
  distinguishes several ways optimization can break the relationship between
  a goal and its proxy. Reconc does not collapse those mechanisms into one
  marketing label.
- The [Anthropic-OpenAI alignment evaluation](https://openai.com/index/openai-anthropic-safety-evaluation/)
  includes a deliberately impossible software task in which an agent falsely
  claims completion. That controlled stress case establishes relevance, not a
  prevalence claim about ordinary coding sessions.
- [in-toto](https://github.com/in-toto/specification/blob/master/in-toto-spec.md)
  and [SLSA 1.2](https://slsa.dev/spec/v1.2/) provide the narrower provenance
  model: a claim is useful only when verification connects it to the process,
  inputs, and artifacts that produced it. Reconc applies that idea to current
  repository state; it does not claim in-toto or SLSA conformance for agent
  sessions.

`Evidence freshness` is Reconc's operational term, not a new reward-hacking
definition. Command success is current only while its recorded write epoch or
staged-candidate fingerprint still matches the state being judged. A later
relevant write invalidates the earlier result. A self-reported claim can be an
input to policy, but it is not equivalent to a command receipt, Git identity,
typed TASK state, or completion proof.

Public verbs have fixed meanings:

- **prevents** only when a controlled boundary cannot accept the action under
  the stated configuration;
- **blocks** when a policy, hook, Git, CI, TASK, or completion decision returns
  a blocking result;
- **detects** when Reconc reports a repository-visible condition but may not
  own a synchronous enforcement point;
- **constrains** for the combined reduction of allowed repository actions and
  accepted completion claims;
- **does not protect against** for uninstrumented hosts, external systems,
  semantic defects outside configured checks, policy chosen by an untrusted
  owner, or a hostile same-user process able to replace policy, state, hooks,
  binaries, or self-reported inputs.

The verified failure-mode map is:

| Failure mode | Truthful Reconc control | Executable proof path | Residual limitation |
| --- | --- | --- | --- |
| Premature victory claim | `reconc done .` **blocks** while policy, candidate, evidence, or typed TASK state is incomplete. | `internal/completiongate/gate_test.go:TestTypedTaskCompletionAndRequiredEvidence`; demo `done` occurs only after `complete-task-evidence`. | Configured checks can still omit a semantic requirement. |
| Incomplete or stubbed implementation | Opt-in `source_hygiene` assurance **detects and blocks** changed shipped source containing supported debt markers or unimplemented sentinels. | `internal/assurance/source_hygiene_test.go:TestSourceHygieneFindsHighSignalShippedCodeDebt`; run the selected assurance pack through `reconc check` or `reconc ci`. | It is a bounded lexical and language-aware gate, not proof that all implementations are substantive. |
| Skipped required work | Required reads, commands, files, claims, coupling, and assurance rules **block** when configured evidence is absent. | `internal/runtime/evaluator_test.go:TestCheckRequireCommandSuccess`; `reconc next .` returns the exact missing action. | Reconc enforces declared requirements; it does not invent the project's true acceptance criteria. |
| Workflow deviation | Installed runtime hooks and git pre-commit **block** supported events that violate compiled policy. | `internal/runtime/agentsession/handlers_test.go:TestRunPreToolUseEnforcesPolicyForbidCommandBeforeExecution`; `reconc hook status . --json` proves configured and observed routes. | Host capabilities differ, timeouts may be fail-open, and an uninstalled or bypassed hook is not enforcement. |
| Scope drift | Path, read, command, and coupling rules **constrain** the configured change surface. | `internal/runtime/evaluator_test.go:TestCheckScopedRuleOnlyFiresInsideScope`; `reconc check . --write PATH`. | Policy cannot infer unstated scope or govern actions outside the repository. |
| Protected write or deletion | `deny_write`, generated-output, secret-state, and destructive-command rules **block** at supported PreToolUse, Git, CI, or completion boundaries. | `internal/runtime/agentsession/handlers_test.go:TestRunPreToolUseBlocksApplyPatchDenyWrite`; demo `protected-action`. | A hostile same-user process can bypass or replace local enforcement. |
| Stale or fabricated self-reported evidence | Causal write epochs, bounded session state, proof validation, and unresolved-block receipts **detect or reject** inconsistent evidence. | `internal/runtime/evaluator_test.go:TestCheckRequireCommandSuccessRequiresFreshCausalEvidence`; `internal/assurance/assurance_test.go:TestSubstantiveProofRejectsFabricatedActualAndFailedThreshold`. | Opaque claims remain only as trustworthy as their configured producer; protected external CI is required against a hostile actor. |
| Test-before-final-change laundering | Staged command receipts bind success to exact HEAD and index identity; later candidate changes **invalidate** the receipt. | `internal/commandproof/proof_test.go:TestProofBindsSuccessToCurrentStagedIndex`; `reconc exec . --staged -- COMMAND` followed by `reconc ci . --staged`. | Unstaged session evidence has a different, write-epoch-based trust boundary. |
| TASK-lifecycle bypass | Typed parsing, transition checks, recoverable transactions, and final completion checks **block** malformed or unfinished TASK state. | `internal/tasklifecycle/tasklifecycle_test.go:TestPromoteArchivesAndActivatesNext`; `reconc task validate .` and `reconc task check-done .`. | Humans and project agents still own scope, priority, acceptance, and evidence quality. |
| Cross-session context drift | `session-briefing` and repository-bound TASK/run state **constrain** reentry to current machine-readable state. | `internal/tasklifecycle/tasklifecycle_test.go:TestInspectSectionsAndBoundedBriefing`; `reconc session-briefing . --json`. | Reconc supplies bounded state, not complete conversational memory or semantic intent. |
| Inconsistent multi-agent compliance | One compiled policy and registry-backed adapters **constrain** supported agents to the same decision engine. | `internal/cli/surface_parity_test.go`; `reconc hook status . --json` distinguishes installed, configured, live, degraded, and unsupported routes. | Adapter presence is not live execution proof, and host enforcement capabilities are not identical. |
| No-progress continuation loop | Per-session material fingerprints and bounded counters **detect and release** repeated continuation without progress. | `internal/runtime/agentsession/repository_run_hotpath_test.go:TestRepoRunNoProgressGuardReleasesOneStopWithoutDisabling`; `reconc run status . --verbose`. | The guard bounds Reconc-controlled continuation; it cannot stop an independent external loop. |

The public narrative stays consistent at three depths:

1. **10 seconds:** An agent saying "done" is a claim. Reconc checks current
   repository evidence before accepting it.
2. **30 seconds:** `reconc demo` shows `protected-action`, `missing-proof`, and
   `remediation`, then runs `real-test`, re-evaluates the corrected state, and
   accepts `done` only after the TASK and proof agree.
3. **2 minutes:** Reconc compiles repository-owned policy, applies the same
   decision engine across CLI, hooks, Git, CI, TASK state, and run control,
   binds command success to the candidate it verified, and exports a portable
   proof bundle. It proves only that the configured repository contract and
   recorded current evidence agree, not that the model is honest or the code
   is universally correct.

Submitted Build Week video, Devpost text, and the immutable v0.8.6 artifacts
remain historical evidence. Current README and documentation use this
terminology; future repository descriptions, release notes, demo captions, and
social posts should derive from this section instead of retroactively editing
the submission or copying a second long-form explanation.

## Install And Build

Requirements:

- Go `1.26`
- Git for `reconc ci` and hook installation
- Bun `1.3.14` for executable OpenCode and Kilo Code adapter tests only; the
  shipped Reconc binary has no Bun runtime dependency
- On Windows, `sh` on `PATH` for generated shell hook wrappers plus `.sh` and
  extensionless policy scripts; Git for Windows supplies it. Native `.exe` and
  `.com` policy scripts execute directly.

Common commands:

```bash
make test
make vet
make lint
make build
go run ./cmd/reconc --help
make self-host
make publication-audit
```

The canonical Make targets cover both the root Go module and
`harness/template`; `make test` additionally runs the publication boundary and
release-trust failure-path checks. Direct `go test ./...` validates only the
root module.

Make targets:

```bash
make build
make test
make vet
make lint
make cover
make bench
make self-host
make publication-audit
make sbom VERSION=0.8.7
make release VERSION=0.8.7
```

`make release` cross-compiles five binaries into `dist/`, copies the native
POSIX and Windows installers, generates three flat shell-completion artifacts,
generates a man page, copies the six immutable v1 schemas plus the current v2
policy-lock schema, generates deterministic SPDX 2.3 and CycloneDX 1.6 SBOMs,
and writes `dist/SHA256SUMS`. The target stops on the first build, SBOM, or
checksum failure. The release verifier requires exactly those twenty
checksummed artifacts, rejects missing, extra, duplicate, unsafe, or corrupted
entries, and never accepts an empty manifest. It regenerates Bash, Zsh, Fish,
and the versioned man page from the current canonical command metadata and
rejects even freshly checksummed artifacts whose bytes are stale or
noncanonical. `dist/` is ignored and should not be committed.

Every host and release build embeds a deterministic version, target, and
production-source digest. The build verifies that marker directly from the
finished binary bytes without executing the artifact. Timestamps, test-only
files, and physical checkout paths do not affect the digest; target-selected
production Go files, embedded assets, `go.mod`, and `go.sum` do.

The stdlib-only SBOM generator inventories both repository Go modules with
`go list -m -json all`, resolves their selected dependency graph, and records
the Go toolchain, release version, full commit ID, and commit timestamp. SPDX
exposes the commit in its document namespace; CycloneDX exposes it as the root
component property `reconc:source-commit`. Fixed inputs produce byte-identical
documents. Verification regenerates both files from the tagged source and
rejects missing, malformed, stale-version, or otherwise non-identical output
before checksums and provenance are accepted.

`install.sh [VERSION]` supports macOS and Linux. `install.ps1 [VERSION]`
supports Windows x64 and defaults to the user-writable
`%LOCALAPPDATA%\Programs\Reconc\bin`; it reports the exact user-PATH command
instead of elevating or weakening PowerShell execution policy. Both installers
download the exact platform binary and published `SHA256SUMS` over HTTPS,
require exactly one matching hexadecimal SHA-256 entry, verify the payload
before executing it, stage and re-verify it inside the install directory, then
atomically replace the target where the platform supports replacement. A
download, manifest, checksum, execution, staging, or publication failure leaves
an existing installation untouched. Windows arm64 remains unsupported until
the release matrix ships a matching native asset.

The immutable v0.8.7 tag contains both `install.sh` and `install.ps1`. Public
bootstrap commands fetch the appropriate script from that tag, never from
mutable `main`, and install the matching checksummed v0.8.7 binary.

When the GitHub CLI (`gh`) is available, the installer additionally verifies
the downloaded binary against its GitHub build-provenance attestation before
installing, which breaks the binary-and-manifest-share-one-origin loop (the
manifest is bound transitively through the checksum comparison).
Verification is skipped with a note when `gh` is absent and downgraded to a
warning when it fails; `RECONC_REQUIRE_ATTESTATION=1` makes both cases fatal.
`RECONC_ATTESTATION_TOOL` and `RECONC_ATTESTATION_REPO` override the tool and
repository for mirrors.

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

Inspection is read-only and reports stack evidence, package managers,
repository markers, same-directory package-manager ambiguity, and detected or
installed platform truth. Planning is read-only unless an explicit output path
is supplied. The `minimal` profile owns policy, a managed AI-orientation block,
and runtime ignores. The `governed` profile adds the TASK control plane,
documentation, `start.md`, and the stable repo-local hook wrapper. Both use
`default` and `agent` as profile defaults. Stack and platform detection produce
suggestions only; packs and hooks remain explicit.

The `existing` profile is the mature-repository wiring path. It requires an
already fresh compiled policy lockfile, rejects pack selection, and owns only
explicitly selected hooks, the repo-local wrapper, and an optional stable
binary. It never owns `.reconc.yml`, agent instructions, docs, TASK files, or
ignore policy. This lets an agent install or refresh universal wiring without
forcing the governed scaffold over a repository's existing control plane.

Plans are deterministic JSON with a format version, product version, canonical
repository root, normalized selections, sorted actions, hashes, modes,
conflicts, compilation state, blocking issues, and a plan digest. Plan output
is create-only. An existing byte-identical plan is unchanged. Explicit
`--replace-output` replaces only a strictly valid Reconc plan for the same
canonical repository and refuses arbitrary or cross-repository files.

Apply publishes only absent targets. Exact artifacts remain unchanged. A
different file, directory, symlink, or special target produces a
hash-addressed `.reconc-candidate-*` artifact and no normal target is installed.
A stale plan fails before publication. New files are staged beside the target,
synced, checksum-verified, and published without replacement. On failure,
rollback removes only transaction-owned files whose file identity and checksum
still match, plus transaction-created directories that are still empty.
Verification is read-only and checks artifacts, lockfile freshness, selected
hooks, governed TASK state, and selected binary resolution.

Successful apply writes a tamper-evident install receipt, reports
created/preserved/drifted/skipped and installed/configured/live counts, and
prints exactly one next command. A stale saved plan prints the exact
selection-preserving replan command with `--replace-output`.

Binary installation has no network path. `--install-binary` uses the running
executable; `--binary PATH --checksum SHA256` accepts an explicit local artifact
and optional `--platform OS/ARCH`. Installed artifacts use the stable
`reconc-<os>-<arch>[.exe]` name. Resolution prefers the stable name, permits
exactly one compatible versioned fallback per searched directory, and fails on
ambiguity before trying development binaries or PATH.

`reconc bootstrap remove --plan PATH` reverses only receipt-owned artifacts.
It removes exact owned files, strips marker-owned blocks, preserves ambiguous
content, emits review candidates for drift, and retains a shared hook wrapper
while another installed platform may need it. `reconc hook uninstall KIND .`
removes one selected platform with the same ownership discipline.

`reconc bootstrap .` remains a compatibility shorthand for a create-only
minimal plan with detected hook directories. It rejects `--force`; drift must
be reviewed through candidates. When all changed bytes are one recognized
managed block, the command offers an exact `--accept-managed-blocks` rerun.
That explicit rerun revalidates target and candidate bytes before accepting
only the managed section. The detailed AI tutorial and manual advanced harness
path remain in `harness/template/BOOTSTRAP.md`.

## Daily Workflow

Before onboarding a repository, `reconc demo` provides a complete local product
proof in under a minute. It creates a disposable isolated Git repository and
runs the current Reconc binary through real compile, block, persisted
remediation, command-success, corrected evaluation, typed TASK, and
evidence-complete `done` paths, then exports and verifies the same candidate as
a portable proof bundle. It makes no network call, bundles or simulates
no LLM, and cleans the workspace by default. `--keep` preserves inspectable
policy, TASK, completion, and proof-bundle artifacts; `--json` exposes the
versioned steps, decisions, durations, artifact hashes, completion and portable
proof digests, and result digest.

```bash
reconc demo
reconc demo --keep
reconc demo --json
```

Onboard a target repository once:

```bash
reconc bootstrap .
```

Then use the canonical daily loop:

```bash
reconc session-briefing . --json
reconc check . --write path/to/file
reconc next .
reconc done .
```

`session-briefing --json` is the bounded machine handshake for session entry
and reentry. Its versioned compact contract combines current TASK/Sub-Task,
policy delta, required evidence, exact remediation, and durable repository-run
state without Git or writes. Static reference material stays on demand through
`reconc agent-intro --section NAME` instead of inflating every agent prompt.

`reconc next [PATH]` loads the latest persisted blocking decision for the
explicit or normally discovered repository. Stale or missing decision state
fails with an exact replay remediation instead of claiming there is no work.

`status`, `doctor`, `verify`, `check`, `ci`, `assert`, `can`, `why`,
`task status`, `task validate`, `task check-done`, `run status`, `run log`,
`session-briefing`, `post-task-check`, `done`, `proof`, and `tui` never compile or write
the lockfile. Missing, stale, malformed, schema-drifted, or non-portable current
lockfiles fail closed with one explicit remediation: `reconc refresh .`.
When `RECONC_AUDIT=1`, enforcement commands may still append decision records;
that opt-in audit write is independent of policy refresh. Explicit `check`,
`ci`, `post-task-check`, and `done` decisions may also write or clear one
private unresolved-block receipt below `RECONC_HOME`; governed worktree content
remains untouched.

The v0.8.7 release can export the same completion candidate for external
review:

```bash
reconc proof . --output proof.json
reconc proof . --format markdown --output proof.md
```

`proof` runs the non-persisting completion evaluation and emits a deterministic
format-1 bundle. It binds build provenance, policy digest, candidate
fingerprint, Git HEAD/index/worktree identity, typed TASK state, stable checks,
current untampered command receipts, required evidence, policy violations,
remediation, and an older unresolved block only when the current candidate
supersedes it. JSON is canonical; Markdown is rendered from the same verified
typed data. A blocked candidate still emits a valid bundle and exits 2.

The exporter never refreshes policy, runs a missing command, writes repository
state, persists a policy decision, or treats absent evidence as success. It
emits `repo_root: "."`, repository-relative slash paths, normalized timestamps
by omission, bounded arrays/text, and no prompts, transcripts, session IDs,
environment values, usernames, home paths, or raw command arguments. Command
receipts expose a redacted executable summary plus a SHA-256 identity of the
normalized full command. `--output` atomically writes the exact stdout bytes.
The public schema is `schemas/v1/proof-bundle.schema.json`.

Exit codes:

- `0`: pass, warn, or informational success
- `1`: runtime or input error
- `2`: blocking policy violation

## FAQ

### What is Reconc?

Reconc is an offline repository control and evidence layer for AI coding
agents. It compiles repo-local rules into a deterministic lockfile, evaluates
real read/write/command/claim evidence, and exposes the same decision contract
to its CLI, Git hooks, native agent hooks, CI, TASK workflow, run control, and
final `done` gate. It is not a coding agent, chatbot, model router, hosted
service, or operating-system sandbox.

### What is the threat model?

Reconc protects honest and fallible agent workflows against missed reads,
out-of-scope writes, stale tests, unsupported claims, skipped TASK work, and
incorrect completion. It treats hook payloads as untrusted and fails closed on
malformed policy, lockfile drift, unsafe paths, and ambiguous managed files. A
hostile same-user process can still replace local policy, hooks, state, or the
binary; use an external sandbox plus protected remote CI when that actor is in
scope.

### Which stack-aware assurance packs ship with Reconc?

Reconc ships 18 opt-in assurance packs for Go, Rust, Python, Java, C#, C/C++,
PHP, Elixir, Zig, Shell, PowerShell, TypeScript, Next.js, Svelte, npm, pnpm,
Yarn, and Bun. Stack detection may recommend a compatible pack, but never
selects one automatically. Packs reuse commands and toolchains declared by the
repository; they do not install dependencies or invent missing test, lint,
build, or typecheck commands. The exact gate contracts and pack-specific
evidence rules are documented in [Policy Packs And Native Assurance](#policy-packs-and-native-assurance).

### Does the product require a model, daemon, Docker, or network?

Not for core repository control. The shipped Go binary runs policy
compilation, evaluation, hooks, run control, and proof generation without a
model, daemon, Docker, Node, Python, or network access. The explicit
`reconc grok` command launches the external Grok ACP process, which owns model
authentication and inference traffic. Codex and GPT-5.6 contributed to
development during OpenAI Build Week but are not runtime dependencies.
Installation, Git operations, and remote CI naturally use the network when
invoked.

### Which agent runtimes are supported?

The registry owns integrations for Claude Code, Codex, GitHub Copilot, Cursor,
OpenCode, Devin CLI, Antigravity CLI, Kilo Code, and Grok Build, plus git
pre-commit as the repository backstop. Host capabilities differ: some expose
synchronous Stop, GitHub Copilot timeouts are host-fail-open, OpenCode and Kilo
expose inferred `session.idle`, and Grok has a strict ACP driver for
continuation. Run `reconc hook status . --json` before claiming that a
particular installation is live.

### How do I install and test it?

Use the immutable v0.8.7 POSIX installer for macOS or Linux and the immutable
v0.8.7 PowerShell installer for Windows x64. Put the installed binary on
`PATH`, then
run `reconc demo` for a network-free, disposable proof of the real
block-to-remediation-to-completion journey. Contributors building current
source can use `go build -o reconc ./cmd/reconc`.

### How does repository bootstrap work?

`reconc bootstrap .` is the compatibility minimal path. The explicit workflow
is `bootstrap inspect`, `profiles`, `plan`, `apply`, `verify`, and `remove`.
Planning is deterministic; apply is transactional and create-only; drift gets a
hash-addressed review candidate; removal touches only receipt-owned bytes. Use
the `existing` profile for mature repositories that already own their policy,
instructions, docs, and TASK state.

### What exactly does `reconc done` prove?

It binds the current policy lock, Git HEAD, index, worktree, active-session
evidence, saved policy report, unresolved blocks, staged command proofs, and
typed TASK completion into one versioned, self-digested report. It does not
accept elapsed time as evidence. `--require-clean-git` optionally adds a clean
worktree requirement.

### What can I share with a reviewer?

Use `reconc proof . --format markdown` for a human review or the default JSON
for automation. Both outputs represent the same current completion evidence,
remain verifiable through their digest, and deliberately exclude private agent
session material and raw command arguments. A BLOCK bundle is evidence of an
unresolved gate, not a failed exporter.

### Does Reconc invent or manage my project backlog?

No. Reconc validates and atomically mutates a configured typed TASK control
plane, but humans or project agents still decide priorities, scope, acceptance,
and evidence. `task new|claim|block|resume|split|promote|archive|recover` preserve
the configured grammar and fail closed on ambiguous state.

### What does the run loop do?

`reconc run on .` enables a durable repository-scoped continuation switch for
supported agents when the TASK plane has executable work. Stop hooks continue,
claim, block, or terminate from typed state; bounded no-progress guards prevent
endless repetition. The agent owns `run on|status|off`; `run reset` is the
explicit recovery path for corrupt or foreign state.

### What are the Windows limitations?

The Go binary and `.exe` or `.com` policy scripts run natively. Shell hook
wrappers plus `.sh` and extensionless policy scripts require `sh` on `PATH`;
Git for Windows supplies it. CI runs the native Windows unit suite, while the
clean-repository self-host golden path currently runs on Ubuntu and macOS.

### Is the private production repository public?

No. Reconc is dogfooded in a large private codebase for an agentic enterprise
platform being built for the author's startup. Public claims use only generic
behaviour and sanitized aggregate evidence. This standalone repository does not
claim byte-identical parity or disclose private source, architecture, prompts,
or task details.

## Troubleshooting

Start with read-only diagnostics:

```bash
reconc status .
reconc doctor . --deep
reconc verify .
reconc hook status . --json
reconc task validate . --json
reconc run status . --verbose
```

If policy sources changed, run the exact remediation `reconc refresh .`; read
commands never refresh implicitly. If a block is valid but unclear, run
`reconc next .`. For a saved transactional bootstrap, use `reconc bootstrap
verify --plan PATH --json`. Use `reconc task recover .` only for an interrupted
TASK transaction and `reconc run reset .` only for corrupt or foreign run
state. Do not delete lockfiles, receipts, managed blocks, or runtime state as a
generic repair strategy.

## Upgrading

Upgrade the binary from one trusted release source, verify the release checksum
and optional GitHub attestation, then run:

```bash
reconc version --json
reconc status .
reconc doctor . --deep
reconc refresh .
reconc hook status . --json
```

`refresh` performs registered lockfile migrations and recompiles current policy;
it never silently rewrites source rules. Re-run an explicit bootstrap plan when
generated hook artifacts need an update, review every drift candidate, and
commit the refreshed portable lockfile in governed target repositories.

## Uninstall And Remove

Prefer receipt- and registry-owned removal:

```bash
reconc bootstrap remove --plan .reconc/bootstrap-plan.json --json
reconc hook uninstall codex . --json
```

Bootstrap removal verifies the saved receipt and current hashes, strips only
marker-owned blocks, preserves shared wrappers, and refuses drift. Repeat
`hook uninstall` only for separately installed runtime kinds. Remove the Reconc
binary from the exact install directory you selected. Reconc intentionally does
not delete user-owned `.reconc.yml`, `AGENTS.md`, TASK files, or policy source;
review those manually. `reconc prune .` bounds owned runtime residue but is not
a substitute for uninstalling policy or user data.

## Development Control Plane

Governed target repositories use `docs/tasks.md` as the durable control plane
for current work and link to one detail file per TASK under `docs/tasks/`.
Completed details move to `docs/tasks/done/`; the overview keeps only the ten
newest completed TASKs visible. The standalone Reconc source repository keeps
its own implementation queue at those paths locally and Git-ignored; it is not
part of the published product state.

TASK state uses `[ ]` for queued, `[~]` for at most one active TASK, `[!]` for
blocked, and `[x]` for done. Each detail records motivation, measurable
acceptance, sub-tasks, temporary notes, and deviations. Runtime task tracking
may assist within one session, but it never replaces these repository files.

Repository harness TASKs bind every non-`none` `Spec Lines` entry one-to-one and
in the same order through `Spec Bindings` using
`docs/spec.md:Lx-Ly@sha256:<range-digest>@term1+term2`. The SHA-256 pins the
exact LF-normalized range bytes, and at least two meaningful terms must occur
in both the TASK claim surface and cited range. `Spec Lines: none` requires
`Spec Bindings: none`. The task-state audit rejects missing, malformed,
duplicated, reordered, stale, out-of-range, or lexically unsupported bindings.
This deterministic check detects byte drift and gross semantic mismatches; it
does not replace the repository's human line-by-line spec-parity review.

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
Portable workflow audits reserve `docs/tasks/` and `docs/tasks/done/` for
referenced `TASK-NNNN-Name.md` details. An unreferenced conforming detail keeps
the atomic `promote-task-done` remediation; any other Markdown file is reported
as a non-TASK file and must be moved outside the reserved tree. `.gitignore`
cannot clear this filesystem audit.

`reconc done` and `reconc post-task-check` share one evidence-complete final
gate. A versioned, self-digested completion report binds the policy lock, Git
HEAD and logical index, worktree contents, dirty paths, active-session evidence,
saved session report, current policy result, current staged command proofs, and
typed TASK completion. Candidate state is captured before and after evaluation;
any change aborts the gate. A blocking explicit `check` or `ci` decision for the
same candidate remains unresolved until a later explicit non-blocking decision
clears its tamper-evident receipt. Time and retention never clear it. With no
typed TASK lifecycle, the TASK check is a minimal pass; configured lifecycle
state must be terminal and satisfy every required section, evidence field, and
optional committed-control-plane rule. `--require-clean-git` adds a clean-tree
check. `--window` is compatibility-only and never changes the decision.

`reconc task status|validate|check-done` are read-only. `new`, `claim`, `block`,
`resume`, `split`, `promote`, and `archive` serialize through a cross-platform
lock and publish one integrity-checked transaction. `task new` adds a
collision-free queued row and grammar-correct detail for either profile without
rewriting existing rows. `task block --no-next` explicitly suppresses the
normal successor auto-claim. `split` accepts only
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
third command supplies current evidence. Session evidence carries causal write
epochs: a successful command recorded before a later matching write is stale
and must be rerun. Later writes outside the rule trigger do not invalidate it.
Explicit `--command-success` evidence applies to the complete evaluation snapshot.

## Command Surface

Daily:

- `demo` - run the isolated real-policy product journey
- `bootstrap` - inspect, profile, plan, apply, verify, and safely remove onboarding
- `status` - one-line policy health summary
- `check` - evaluate runtime evidence against compiled policy
- `next` - show the next remediation
- `done` - task-finish gate
- `proof` - deterministic portable completion proof

Bootstrap and inspection:

- `bootstrap inspect|profiles|plan|apply|verify|remove`
- `init`
- `adopt`
- `extract`
- `doctor`
- `verify`

Compile and evaluate:

- `compile`
- `refresh`
- `ci`
- `exec`
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
- `grok`

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

In governed target repositories, repo-local policy lives in `.reconc.yml` and
should be committed. The generated `.reconc/policy.lock.json` is a portable,
committable policy contract and should be reviewed with policy-source changes.
Format 2 is checkout-independent and byte-identical across equivalent clones
and worktrees. Its `lock_digest` binds the complete canonical payload except
for the digest field itself, and runtime also verifies that embedded rules
equal the rules parsed from the current policy sources. Format-1 lockfiles are
migrated in memory after their legacy schema identity is validated. Publication
uses atomic replacement and skips the write entirely when the canonical bytes
are unchanged, so readers never see partial JSON and repeated compiles do not
create needless filesystem churn. This standalone product repository does not
carry either file and must exercise policy compilation only inside isolated
test repositories. Its ignore patterns remain as a defensive boundary against
accidental local state and for nested bootstrap fixtures.

Policy authoring is strict. Unknown keys at the document, scope, rule,
evidence, composite-check, and TASK-lifecycle levels fail compilation instead
of being ignored. This validation applies only to structured YAML fields;
free-form rule messages and agent prompts remain unrestricted text. Editors and
automation can use `schemas/v1/policy-config.schema.json`; emitted lock, policy
report, completion report, fix-plan, and proof-bundle artifacts keep their separate public
schemas.

The managed target-repository block uses these exact rules. It ignores mutable
runtime state while explicitly re-including `.reconc/policy.lock.json`:

- `/tools/reconc/dist/`
- `.reconc/*`
- `!.reconc/`
- `!.reconc/policy.lock.json`
- `.reconc/audit.jsonl*`
- `.reconc/cache/`
- `.reconc/locks/`
- `.reconc/reports/`
- `.reconc/run/`
- `.reconc/sessions/`
- `.reconc/task-transaction.json`
- `.reconc/bootstrap-*.json`
- `*.reconc-candidate-*`
- `*.reconc-remove-candidate-*`

The standalone source repository additionally ignores defensive nested-fixture
equivalents; the complete source-repository list is in Git Ignore Policy.

Runtime retention is product-owned rather than harness-owned. `SessionStart`
and `SessionEnd` run a cross-process-safe due check with a six-hour interval;
Stop never prunes. `reconc prune [repo] [--dry-run] [--json]` runs the same
core explicitly. Unchanged session files, active-session pointers, reports,
command proofs, and run state are byte-compared and never republished. Disabled
and unchanged hook events do not create run state. Run decisions record every
bounded repository continuation plus material transitions without prompt
payloads. Live session state is hard-capped at 1 MiB; every evidence
collection has both item and byte limits, and repeated command results are
deduplicated. Reaching a collection limit seals the complete raw evidence into
an immutable bounded segment before accepting the triggering event. Segments
bind the canonical repository and session identities, policy-lock hash, index,
and previous segment digest; every policy, claim, CI, Stop, and completion
consumer verifies and replays the full chain plus the live segment. A session
may seal at most 64 segments. Clean SessionEnd removes its raw segments after
verifying the chain. Each full replay builds its string and command-result
identity indexes once, so deduplication remains linear in total evidence across
all sealed and live segments.

An event that cannot fit an empty segment, a 64-segment exhaustion, or a
missing/corrupt segment creates a project-scoped evidence taint. The taint
records the exact field and limit cause (`item_bytes`, `item_count`,
`byte_budget`, `serialization`, `segment_count`, `segment_storage`, or
`chain_integrity`), survives process reload and SessionEnd, and is inherited by
successor sessions. Taint blocks repository writes, commands, MCP material
actions, claims, CI, policy passes, and completion; read-only diagnosis remains
available. With repository run enabled, Stop remains blocked. With repository
run disabled, Stop records `uncertified_termination` and releases the host
without representing the session as certified. An explicit user interrupt
continues to release the host invocation, while the durable taint remains.
Recovery is explicit: end the active session, inspect
`reconc hook evidence-status .`, then pass its exact token and an operator
reason to `reconc hook evidence-resolve . --token TOKEN --reason TEXT`. Reconc
writes an immutable resolution receipt before removing the live taint; the next
session starts a new evidence window and must reproduce every required proof.
The latest unresolved policy block is retained without an age limit and also
protects its project-state root from global cleanup. A validated non-blocking
decision removes that receipt durably; retention never converts block to pass.
Agent persistent-memory writes (`$CLAUDE_CONFIG_DIR/projects/<project>/memory/**`,
defaulting to `~/.claude/projects/<project>/memory/**`) are harness runtime
state, not repository writes. The pre-tool gate excludes only the current
repository's canonical project key plus its Git-common-dir/worktree aliases
from the repo write policy. Unrelated project memory remains gated. Resolution
is filesystem-identity-hardened, including Unix symlinks, Windows junctions,
component-wise Windows 8.3/long-path alias mixtures, and first writes below a
not-yet-existing leaf.
A memory-looking path that resolves elsewhere stays gated; accepted memory
writes are never recorded as repository write evidence.
Host session IDs are validated exactly and mapped to collision-resistant file
keys. State, reports, active pointers, and locks use bounded reads, private
permissions, atomic publication, and cross-process locking; legacy sanitized
paths migrate only after their stored identity and repository binding pass.

Default persistent budgets are 32 session files / 8 MiB / 14 days, 32 reports
/ 8 MiB / 14 days, 128 locks / 1 MiB / 24 hours, 64 staged command proofs /
256 KiB / 24 hours, 16 MiB total external state, and 32 MiB / 14 days for
generated audit binaries. The product-wide project
state root is independently bounded to 256 recognized project roots, 128 MiB,
and 30 days. Explicit prune enforces that global bound immediately; lifecycle
passes protect the current project, live sessions, and roots touched within the
24-hour concurrency grace. Unknown directories are never treated as
product-owned. Audit and run-decision JSONL each use a 2 MiB live file plus two
archives, with file-locked append and
pre-append rotation. Repo runtime is capped at 48 MiB. Known
`reconc-proof-neg-*`, `reconc-proof-neg-copy-*`, and
`reconc-proof-gocache-*` temp trees are removed after a two-hour inactive
grace, retaining recent work while removing hard-kill residue before a full
working day passes. Active session/report/lock files, live build-lock targets,
run state/locks, and recent temp trees are never deleted to force a budget.
Global temp and project-root scanning use independent six-hour markers, so
multiple repos do not re-walk either tree on every session start.
Durable publication and CLI output paths propagate write, sync, close, and
unlock failures instead of reporting partial output as success.

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

Command matching is exact (normalized whole strings) by default.
Normalization is wrapper- and anchor-tolerant without weakening semantics: a
transparent `rtk ` proxy prefix is stripped at command position, an absolute
repo path inside `cd` becomes repo-relative, and a leading `cd <repo-root> &&`
(or `;`) anchor collapses away entirely because it is a no-op inside the repo
(`||` and pipe joins are never collapsed). Session write epochs recorded under
absolute payload paths are aliased to repo-relative spellings during `ci`, so
the `require_command_success` write-epoch freshness contract binds instead of
silently reading zero. In `ci --staged`, `require_command_success` violations
name the exact index-bound remediation (`reconc exec <repo> --staged --shell
-- '<command>'`) because the staged gate accepts only index-bound proofs,
never session command history. The command
kinds accept an additive `command_match: prefix` opt-in that also matches a
recorded command extending an expected command at a token boundary
(`pip install` matches `pip install requests`, never `pip installer`); for
`require_command_success` that includes runs with extra arguments such as a
`-run` filter, so authors opt in deliberately. Ordering semantics differ by
design: `require_command` is presence-only (the command may have run before
the triggering write), while `require_command_success` additionally enforces
the write-epoch freshness contract. After upgrading, existing repositories
must re-run `reconc hook install` for `claude-code`, `codex`, `opencode`, and
`kilo` once to pick up the current host event contracts, matchers, bounded
timeouts, and payload adapters.

Command-prevention checks examine executable shell segments rather than one
flat string. Top-level and composite `forbid_command` rules therefore run at
PreToolUse and cannot be hidden behind `sh -c`, `bash -lc`, literal `eval`,
command substitutions, backticks, process substitutions, command groups,
sequencing, boolean joins, or pipelines. Parsing is quote-aware: single-quoted
text and shell comments stay literal, substitutions inside double quotes
remain executable, leading redirections are skipped, and unquoted backslash-
newline continuations are folded before matching. Common
`env`/`sudo`/`command`, `flock`, and `watch` wrappers plus `find
-exec`/`-ok`/`-execdir`/`-okdir` and `xargs` command launchers are resolved,
while ordinary literal arguments such as `echo git clean` never become
executable-command matches. An unqualified rule executable matches the basename
of an absolute executable path; explicitly path-qualified rules remain exact.
During PreToolUse, a composite violation blocks only when the current command
itself hits a direct `forbid_command`, so historical results and unrelated
failing subchecks cannot poison later safe commands. Recursion is bounded;
unresolved dynamic executable names and exhausted nesting fail closed. The built-in
destructive Git guard uses the same model for `git clean` and `git reset
--hard`.

A stale compiled lockfile blocks gated work, because policy cannot be enforced
from a lockfile that no longer describes its sources. The block does not seal
the session: the PreToolUse route admits a lockfile-repair invocation
(`reconc refresh` or `reconc compile`) even while the lockfile is stale, and the
block message names that escape plus the alternative of reverting the policy
source. The exemption is bounded. It applies only when the failure is the
lockfile contract itself rather than a policy violation, every executable
position in the command must be a repair invocation so a compound command
cannot smuggle work through, analysis must be complete, and the executable must
be `reconc` or a versioned release artifact such as `reconc-0.8.7-darwin-arm64`.
The emitted remediation therefore tells the operator to run the repair as the
only executable command. Piping it to another command or chaining unrelated
work is deliberately refused.
Writes stay blocked while the lockfile is stale, and the hooks never
auto-compile, so an edited policy source can never govern the session that
edited it.

A fail-closed block names the structural cause and the concrete fix instead of
refusing generically. Analysis reports one of `dynamic_command`,
`nesting_depth`, `too_large`, `unparsable`, or `analysis_state`, and the guard
maps each to its own single-line remediation: write the executable as a literal
word, flatten the nesting, split the command, or fix the Bash syntax. When
several causes apply, the first one reached in the fixed AST walk order is
reported, so the message is deterministic for a given command. Only a fully
resolved analysis permits a command; adding cause attribution changed no allow
or block decision.

`reconc adopt .` and `reconc bootstrap inspect .` share deterministic stack
detection for Go, JavaScript, TypeScript, npm, pnpm, Yarn, Bun, Python, Rust,
Shell, C/C++, Java, PHP, C#, Next.js, Svelte/SvelteKit, Zig, Elixir, and
PowerShell. Detection uses conventional
manifests and source extensions through six repository levels, skips
dependency/build trees and symlinks, and may propose the matching
`*-assurance` pack. Next.js and Svelte detection additionally requires their
declared package dependency in a bounded, valid `package.json`; generic React
or package metadata alone does not create a framework recommendation. A
proposal is review-only.
`adopt --apply` adds individual rule suggestions but never mutates `extends`;
the agent or user must explicitly select a pack in `.reconc.yml` after
confirming that its contract fits the repository.
Node package-manager selection uses same-boundary lockfiles and
`packageManager` metadata. Multiple managers at one boundary are reported as
an explicit ambiguity; Reconc does not choose one. Individual Node command
suggestions require both one unambiguous manager and a non-empty matching
`package.json` script. A bare `tsconfig*.json` is stack evidence, not permission
to invent `tsc --noEmit` or any other command.

Assurance packs are opt-in policy bundles, not compilers or dependency
installers. `go-assurance` adds canonical formatting, owned-concurrency,
network/process-boundary, test, and vet evidence; `bun-assurance` adds exact
dependency pins and Bun test evidence. `npm-assurance`, `pnpm-assurance`, and
`yarn-assurance` add exact dependency pins plus current evidence only for the
standard verification scripts each matching package actually declares;
workspace package commands are scoped to their manifest directory.
`typescript-assurance` adds declared typecheck evidence and changed-source
hygiene only where `tsconfig*.json` exists, and explicitly conflicts with the
framework-specific Next.js and Svelte packs to prevent duplicate ownership.
`python-assurance`
adds Python source hygiene plus successful project-native Python or pytest
evidence; `rust-assurance` adds source hygiene plus cargo test, format, and
Clippy-with-warnings-denied evidence. `shell-assurance`, `cpp-assurance`,
`java-assurance`, `php-assurance`, and `csharp-assurance` add changed-source
hygiene plus common project-native verification alternatives for their stacks.
`nextjs-assurance` requires a production build, separate lint evidence,
route-aware `next typegen` plus TypeScript evidence, and source hygiene;
`svelte-assurance` requires a production build, `sv check` or the canonical
project check script, and source hygiene. `zig-assurance`, `elixir-assurance`,
and `powershell-assurance` add their native test/format or analyzer evidence
plus source hygiene. Framework and language packs accept common Bun, npm,
pnpm, Yarn, Zig, Mix, Pester, and PSScriptAnalyzer command forms without
installing any of those tools.
They reuse successful command evidence from the repository's own toolchain,
remain inert until selected through `extends`, and never install or execute a
compiler, framework, package manager, or test runner themselves. Repositories
with different canonical commands can override or supplement the bundled
alternatives. Reconc governance itself remains language-independent:
unsupported stacks use the same path, command, script, template, and evidence
rules without requiring a built-in assurance pack.

The portable builtin template set covers source/test and docs coupling,
generated-output protection, CI claims, authority-change approval, bounded
repo-local gates, local secret/database-state write protection, and current
successful-command evidence. Templates remain inert until a policy references
them and supplies the repository-owned paths, commands, or script where the
shape requires those inputs.

Generic dependency-locality audits exclude supported agent-runtime state trees,
including `.devin/`, `.grok/`, `.kilo/`, legacy `.kilocode/`, and the other
registered platform directories, so plugin dependencies are not mistaken for
product dependency leakage.

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
| `package_scripts` | Every configured script that is actually declared and non-empty has current successful manager-scoped evidence; absent scripts stay optional | Matching package manifests, including inherited workspace manager evidence |
| `network_boundary` | Changed source sites have a nearby non-comment guard marker or reasoned path exemption | Matching changed files |
| `process_boundary` | Changed process-spawn sites have a nearby non-comment hardening marker or reasoned path exemption | Matching changed files |
| `substantive_proof` | Fresh measured samples, computed aggregate, threshold result, live command, and byte-matched evidence agree | Full configured proof manifest |
| `live_verification` | Every or any configured command has current successful evidence | Current session |
| `go_concurrency_boundary` | Changed production Go files contain no unowned bare `go` statements | Matching changed Go files, parsed with the Go AST |
| `go_format` | Changed Go files are byte-identical to Go standard-library canonical formatting | Matching changed Go files |
| `source_hygiene` | Changed shipped source contains no leading implementation-debt markers or language-specific unimplemented sentinels | Matching changed source files |

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
`go_concurrency_boundary` parses only changed matching `.go` files with the Go
standard-library parser and fails closed on invalid source. A local goroutine
is accepted only when the same function proves matching `WaitGroup.Add` before
launch, deferred `WaitGroup.Done` inside the function literal, and
`WaitGroup.Wait` after launch; other ownership models require a reasoned path
exemption. `go_format` uses
the Go standard-library formatter over the same bounded file snapshot and
fails closed on invalid Go. Both run through `go-assurance`; formatting covers
tests while concurrency excludes tests, and both exclude `vendor/**`.

The default `agent` pack is deliberately stack-neutral. It requires agents to
read the repository context before writing and keeps public documentation in
sync with changed public behavior. It does not guess a language, package
manager, source-hygiene policy, test command, or build command. The default
pack handles generated-output boundaries only. Explicit assurance packs own
language-specific hygiene, formatting, concurrency, architecture, performance,
and repository checks.

## Architecture

Pipeline:

```text
repo root -> ingest -> parser -> compiler -> .reconc/policy.lock.json -> runtime -> CheckReport/FixPlan/CompletionReport -> ProofBundle
```

The exhaustive contributor package map is maintained in
[the architecture reference](architecture.md#package-map). The following list
summarizes the core runtime responsibilities:

- `cmd/reconc`: CLI entry point only
- `buildprovenance`: deterministic target/source build identity and byte-only binary inspection
- `internal/cli`: argument parsing and command dispatch
- `internal/demo`: isolated real-command product journey and self-digested proof result
- `internal/ingest`: repository discovery and source loading
- `internal/parser`: YAML-to-policy validation and normalization
- `internal/compiler`: canonical JSON lockfile generation, digesting, conflicts, migrations, compile lock
- `internal/bootstrap`: deterministic inspect/plan/apply/verify/remove transactions, receipts, managed-block acceptance, and binary resolution
- `internal/stackdetect`: shared bounded manifest/source stack discovery
- `internal/runtime`: policy evaluation, remediation, git integration, scripts, templates
- `internal/schema`: canonical format-versioned public JSON schema locations and enterprise URL resolution
- `internal/assurance`: bounded native repository assurance evaluators
- `internal/hooks`: typed hook platform registry, artifact generation, non-destructive install/uninstall, scaffold sync, managed activation, and diagnostics
- `internal/runtime/agentsession`: hook-runtime session state and event handling
- `internal/audit`: opt-in JSONL decision log and rotation
- `internal/atomicfile`: atomic write-on-change publication
- `internal/filelock`: Unix/Windows cross-process file locking
- `internal/grokacp`: strict Grok ACP client plus Unix-socket and Windows named-pipe leader steering/probing
- `internal/jsonl`: bounded, locked JSONL append and archive rings
- `internal/pathidentity`: Unix symlink and Windows reparse-point/8.3 filesystem identity
- `internal/commandproof`: commit-candidate-bound staged command-success receipts
- `internal/completiongate`: final policy, candidate, command-proof, and TASK completion contract
- `internal/proofbundle`: deterministic portable JSON and Markdown completion evidence
- `internal/policyproof`: tamper-evident unresolved policy-decision receipts
- `internal/retention`: runtime storage classes, lifecycle due checks, and cleanup
- `internal/presets`: bundled and user policy packs
- `internal/templates`: bundled and user rule templates
- `internal/tasklifecycle`: typed TASK profiles, validation, bounded briefing,
  recoverable transactions
- `internal/tui`: dependency-free terminal dashboard

Key invariants:

- Deterministic JSON artifacts
- Stable schema and `format_version` fields; immutable v1 contracts live under `schemas/v1/`, while current portable policy locks use `schemas/v2/`; all six v1 schemas and the current v2 lock schema ship in every future release containing the proof exporter
- Fail closed on malformed policy, stale lockfiles, schema drift, invalid globs, unsupported rule kinds, and non-portable current lock envelopes
- No core policy-runtime network calls; explicit `reconc grok` delegates
  authenticated inference to an external Grok ACP process
- Behavior in internal packages, thin `cmd/reconc/main.go`

## Agent Skill

The repo ships one agent-facing skill at `skills/reconc/SKILL.md`.

It is written for Codex, OpenCode, Claude Code, and other coding agents. The
skill documents the same reconc workflow for every agent runtime:

- begin and reenter with the versioned `session-briefing --json` contract
- collect truthful read, write, command, and claim evidence
- use `reconc next .` for remediation
- run `reconc done .` before claiming completion
- distinguish native hook enforcement from CLI self-checks

The typed platform registry is the source of truth for Git pre-commit, Claude
Code, Codex, GitHub Copilot, Cursor, OpenCode, Devin CLI, Antigravity CLI,
Kilo Code, and Grok Build. It owns native event names, normalized lifecycle coverage, compatibility
routes, config and scaffold paths, failure behavior, timeout budgets, output
budgets, installation strategy, and activation probes. `reconc hook status
[repo] [--json]` validates every registered artifact and reports `absent`,
`installed`, `configured`, `degraded`, `shadowed`, or `unsupported`.
`configured` means the static configuration is complete and host-discoverable;
it is not proof that a host process executed it. Separate JSON `expected_events`,
`live_events`, `unseen_events`, `last_seen`, and `last_event` fields report
which registry routes a live runtime actually executed. Liveness is stored
outside the repository and each route writes at most once every six hours.
Human output keeps only the seen/expected count and last event so large route
registries do not dominate the terminal.

### Host Integration Truth

Static configuration, documented discoverability, and live behavior are
different facts. Reconc uses these terms consistently:

| State | Meaning |
| --- | --- |
| `configured` | The managed project artifact is present, semantically current, executable where required, and has all local activation prerequisites. |
| `discoverable` | The host contract scans that artifact path on the named surface. This does not imply a process loaded it. |
| `loaded` | A current host process emitted an initialization or session route attributable to the managed artifact. |
| `observed` | The exact named route emitted current bounded liveness evidence. |
| `enforced` | A disposable negative probe proved the named pre-action route blocked before the requested side effect occurred. Static inspection never produces this state. |
| `inferred` | Reconc maps a weaker lifecycle to a capability, such as OpenCode/Kilo `session.idle` to continuation. It is not native parity. |
| `degraded` | A required artifact, activation, route, identity, API, or live proof is missing or unproven. |
| `unsupported` | The host does not expose the required lifecycle on that surface, or Reconc intentionally has no sound behavior for it. |

`hook status` preserves the public activation enum `absent`, `installed`,
`configured`, `degraded`, `shadowed`, and `unsupported`. Its
`expected_events`, `live_events`, `unseen_events`, `last_seen`, and
`last_event` fields keep static and live truth separate. The repeatable
disposable probe in `scripts/tests/host-integration-probe.sh` adds the
surface-specific `discoverable`, `loaded`, `observed`, `enforced`, `inferred`,
and `unproven_events` facts. It refuses model- or account-using execution
without `--allow-authenticated`, records only route names, timestamps,
structural field names, and outcomes, and never targets this product
repository.

| Surface | Project artifact and eligible contract | Strongest truthful guarantee before a live probe |
| --- | --- | --- |
| Cursor desktop Agent | `.cursor/hooks.json`; installed Agent lifecycle, tool, shell, MCP, subagent, compaction, and Stop routes | `configured` and `discoverable`; each route becomes `observed` or `enforced` independently |
| Cursor desktop Cmd+K | The same Agent-hook entries when Cursor emits the corresponding Cmd+K lifecycle | Shared Reconc route semantics, not blanket Agent parity |
| Cursor inline Tab | `afterTabFileEdit` only; read-prevention and Agent lifecycle are intentionally absent | Successful Tab-write evidence only after that exact route is observed |
| Cursor CLI interactive | The same project file; actual emitted event set is host-version dependent | No IDE/CLI parity claim without event-by-event live evidence |
| Cursor CLI print mode | The same project file under the headless Agent CLI | No interactive/print parity claim; structured CLI output is not substituted for missing pre-action hooks |
| Cursor cloud agents | Repository hooks after a writable environment exists; session start/end, dedicated MCP, and Tab routes are unavailable | Only documented eligible routes; no live claim without approved cloud execution |
| OpenCode CLI | `.opencode/plugins/reconc.js`; prompt, permission, tool, session, compaction, terminal failure, and inferred idle continuation | Static plugin contract plus per-route liveness; continuation remains inferred |
| Kilo Code CLI | `.kilo/plugin/reconc.js` with `KILO_PURE` unset; same lifecycle classes as OpenCode | Static plugin contract plus per-route liveness; continuation remains inferred |
| Kilo Code VS Code host | The same canonical project plugin when that host loads external project plugins | CLI observations are never reused as VS Code proof |

Cursor's registry classifies all 21 current host events exactly once. Reconc
installs 16: `sessionStart`, `sessionEnd`, `preToolUse`, `postToolUse`,
`postToolUseFailure`, `subagentStart`, `subagentStop`,
`beforeShellExecution`, passive `afterShellExecution`,
`beforeMCPExecution`, `afterMCPExecution`, `afterFileEdit`,
`beforeSubmitPrompt`, `preCompact`, `stop`, and `afterTabFileEdit`.
`beforeReadFile` and `beforeTabFileRead` cannot prove successful reads;
`afterAgentResponse` and `afterAgentThought` expand a privacy-sensitive,
non-evidentiary surface; `workspaceOpen` is configuration lifecycle rather
than repository policy. Those five events remain explicit unsupported
dispositions and are not installed as no-ops.

Cursor records positive generic tool evidence only from `postToolUse`.
`postToolUseFailure` records failure without positive read, write, or command
evidence. `afterShellExecution` contains output and duration but no
authoritative exit status, so it records liveness only. `afterFileEdit` and
`afterTabFileEdit` are successful write fallbacks deduplicated against generic
tool delivery. Decision routes return `{"permission":"allow"}` or an exact
deny object; observation routes return `{}`. Stop and subagent Stop use
Cursor's bounded `followup_message` response. A malformed or outcome-unknown
post event cannot satisfy command freshness, completion, or proof.

OpenCode and Kilo shell success is accepted only from an integer
`output.metadata.exit`. Exit zero succeeds. Non-zero, timeout, abort, explicit
error, missing exit, fractional/non-finite/overflowing value, numeric string,
or conflicting result is failure. Stdout and stderr text never decide the
outcome. The adapters copy the validated value into the host-neutral
`tool_response.exit_code` before the Go runtime sees it.

Their `session.idle` continuation is a bounded asynchronous state machine, not
a synchronous Stop hook. It stores at most 1,024 session-safe entries, permits
at most ten accepted continuations per session, suppresses duplicate idle
events until real user/tool activity advances the generation, and never stores
prompts or model output. It calls only
`client.session.promptAsync({sessionID, messageID, parts})`, waits for request
acceptance, and never falls back to synchronous `prompt`. The generated
`msg_reconc_...` identifier correlates only the injected `chat.message`; an
unrelated user message still advances the activity generation even if the
host never reports the injected callback. Missing APIs, rejected requests,
runtime timeout/error, malformed or truncated Stop JSON, and the continuation
cap are fail-open host outcomes with bounded redacted diagnostics. The shared
Bun runner drains stdout and stderr concurrently, enforces one combined 8 KiB
budget, rejects invalid UTF-8, kills and awaits the timed-out subprocess, and
never publishes truncated JSON as a decision.

MCP effects are opt-in compiler configuration:

```yaml
mcp:
  unclassified: deny
  tools:
    - platform: cursor
      tool: repository_write
      effect: repository_write
      path_fields: [/path]
    - platform: opencode
      tool: run_check
      effect: command
      command_field: /command
    - platform: kilo
      tool: external_service
      effect: external
```

Each mapping is an exact `(platform, server_fingerprint, tool)` selector.
Fingerprint presence is part of identity: a fingerprinted call never falls
back to an unqualified mapping, and an unqualified call never matches a
fingerprinted mapping. `path_fields` and `command_field` are exact RFC 6901
JSON Pointers. Repository paths must resolve inside the target repository;
missing, malformed, escaping, or wrong-typed values become unclassified and
produce no positive evidence. `external` calls never become repository
evidence.

Cursor's dedicated MCP pre-hook can enforce `unclassified: deny`. OpenCode and
Kilo expose exact generic tool identities but no reliable discriminator
between an unconfigured MCP tool and a built-in/custom tool, so strict
unclassified deny is unavailable on those generic surfaces. Configured exact
identities remain enforceable. Server locators, credentials, arguments,
results, prompts, and command bodies are not persisted in MCP status/audit.
Use `reconc why mcp .`, `reconc hook status . --json`, and
`reconc doctor . --deep` to inspect the compiled mappings, redacted
observations, and host limitation.

Before any non-Git installer write, Reconc resolves the prospective target
through the operating system's filesystem identity and rejects paths outside
the selected repository. This follows Unix symlinks and Windows reparse points,
including directory junctions, while normalizing Windows 8.3 aliases. Scaffold
sync validates every prospective artifact target before its first write, so one
escaping parent cannot produce a partial or external rollout. Forced
malformed-config backups are content-addressed,
create-only, private (`0600`), file-synced, and parent-directory-synced before
the managed artifact is published.

The registry assigns 5-second observation/session budgets, 10-second pre-tool
and permission budgets, and 30-second Stop budgets instead of one blanket
timeout. Claude, Codex, GitHub Copilot, Devin, Antigravity, and Grok generators
emit those host timeouts; OpenCode and Kilo Code enforce them inside their adapters. Each runtime
route caps combined process output at 8 KiB.
Post-compaction recovery context is deduplicated and capped at 4 KiB.

Claude Code, Codex, GitHub Copilot, Cursor, Devin, Antigravity, and Grok
generated configs use `tools/reconc/bin/hook` on POSIX; the wrapper owns repo-local binary
selection and PATH `reconc` as last fallback.
For development and self-hosting, the wrapper checks `.build/bin/reconc` and
root `reconc` before invoking any platform probe. Otherwise, each
`tools/reconc/dist` and root `dist` directory prefers the stable platform name
and accepts exactly one compatible versioned artifact as a migration fallback.
Multiple compatible versions fail closed before PATH fallback.
The development binaries and all repository-local release binaries remain
writable by the repository owner and are not re-attested on every hook event.
This resolution order is a convenience and availability contract inside the
documented non-hostile same-user model, not a security boundary against a
process that can replace repository files. Use a sandbox and protected remote
CI outside that write authority when hostile same-user replacement is in scope.
Claude Code uses its exec-form
`command`+`args` shape so it does not spawn a hook shell or run a hook-launcher
Git lookup. Codex uses the host shell command string without a nested `sh -lc`;
Cursor and Antigravity use portable shell launchers with a direct
wrapper fast path before their Git fallback.
Codex bootstrap and direct hook installation manage `hooks = true` under the
`[features]` table. Direct installation rejects an explicit user-owned
`hooks = false` before any artifact write unless `--force` is supplied.
Transactional bootstrap reports the change as managed drift and requires the
explicit marker-only acceptance path. Forced or accepted activation records
the exact original line inside the managed block; hook uninstall and bootstrap
removal restore that line byte-for-byte. A root-level `hooks=true` lookalike is
invalid. Codex
does not expose `SessionEnd`; Reconc generates only supported routes and gives
each route its exact 5, 10, or 30 second host timeout. Codex also has no
separate failed-tool event: Reconc classifies non-successful Bash outcomes from
the released `PostToolUse` payload and records them through the failure path.
User prompts, pre/post compaction, subagent start/stop, permission, tool, and
Stop lifecycles are all routed. `apply_patch` is routed through Reconc by
parsing patch headers from `tool_input.command`; a non-empty patch with zero
parseable file operations fails closed instead of silently bypassing the write
gate. GitHub Copilot uses the version-1 repository hook contract at
`.github/hooks/reconc.json`. Copilot CLI and coding agent load the same
repository file, while the coding agent honors only its Linux `bash` command
in `/workspace`. Reconc generates documented PascalCase compatibility events
plus native `subagentStart`, validates `cwd` against the selected repository,
normalizes `tool_result` into evidence, and translates PreToolUse,
PermissionRequest, PostToolUseFailure, Stop, and SubagentStop output into
Copilot's exact schemas. `PermissionRequest` and `Notification` are CLI-only; cloud permission
enforcement therefore uses `PreToolUse`. Missing or failed Stop wrappers emit
an explicit block while Copilot's own timeout behavior remains fail-open. The
managed filename is never overwritten when it contains foreign content, even
with `--force`. Static configuration and contract tests are not live proof;
only per-route liveness in `reconc hook status . --json` can establish that a
host actually executed the adapter. Cursor uses `.cursor/hooks.json` with the
registry-driven, surface-specific outcome contract defined in
[Host Integration Truth](#host-integration-truth). If Cursor also
executes compatible `.claude/settings.json` hooks, Reconc detects Cursor-native
payload markers and no-ops those non-native Claude hook invocations before they
can duplicate Cursor session evidence. Claude routes its native prompt,
permission-denied, failed-tool, Stop-failure, subagent, pre/post-compaction,
and session events. After compaction, the context-capable `SessionStart`
`compact` matcher restores a bounded recovery packet; native `PostCompact` is
retained as an observation event because it cannot inject context. Devin uses
`.devin/hooks.v1.json`, including native `UserPromptSubmit` and
`PostCompaction`, and suppresses compatible Claude-hook duplicates.
Antigravity uses `.agents/hooks.json` with `PreInvocation`, `PreToolUse`,
`PostToolUse`, `PostInvocation`, and `Stop`; Reconc stores Antigravity PreTool
metadata as pending evidence so PostToolUse can record exact evidence when the
post payload only carries a step index/result. OpenCode and Kilo Code use thin Bun
adapters at `.opencode/plugins/reconc.js` and `.kilo/plugin/reconc.js`. They
route `chat.message`, hard pre-tool and permission hooks, complete
`tool.execute.after` title/output/metadata, terminal tool errors from
`message.part.updated`, pre/post-compaction, and session lifecycle. Repeated
terminal error notifications are bounded and deduplicated by tool call. They
translate host events only; policy, session state, compaction context, and
continuation decisions stay in the Go runtime. Exact shell outcomes,
subprocess bounds, and idle-continuation behavior are defined in
[Host Integration Truth](#host-integration-truth). On Windows, the adapters
invoke the extensionless POSIX wrapper through `sh`.
Grok Build uses the dedicated `.grok/hooks/reconc.json` native artifact.
Its camelCase envelopes and native tools (`run_terminal_command`,
`run_terminal_cmd`,
`read_file`, `write`, `search_replace`, `hashline_*`, `grep`, and `list_dir`)
are normalized in Go instead of being routed through a Claude or Cursor
adapter. Project hooks require Grok folder trust. `reconc doctor --deep`
executes `grok inspect --json` when the artifact is installed and verifies
trust plus all 14 generated routes by exact command-token identity; route-name
prefix collisions do not count. Grok runs PreToolUse before its own
permission rules, so Reconc still blocks under Grok's always-approve mode.
Grok itself treats hook crashes, malformed output, and timeouts as fail-open.
The generated PreTool route and repo-local wrapper therefore run a bounded
five-second guard inside the host budget and convert non-zero, timed-out,
empty, multiline, or non-exact decision output into explicit deny JSON. Normal
Reconc allow and deny outcomes pass only when they are one exact valid JSON
object. The hard outer Grok process timeout remains host-owned and fail-open.

Reconc emits exact native `Stop` block JSON in the standard stock TUI without a
leader: it validates `stopHookActive`, marks eligible live Stops strict, and
emits exact `{"decision":"block","reason":"..."}` JSON. Empty clean output
allows completion. Missing, broken, or ambiguous binaries, malformed payloads,
runtime failures, and invalid non-empty Stop output become block JSON while the
wrapper can still respond. The generated Stop route uses a 600-second budget;
host timeout or OS kill before any wrapper output remains Grok-owned fail-open
behavior. Reconc treats the Stop output as synchronously enforced only when the
hook guide shipped with the installed Grok distribution contains both a
blocking Stop event row and `Stop Decision Control`; it never infers the
capability from a version string. When advertised, the host's documented
re-entry and continuation bound apply. Explicit user interrupts, API failure,
max-turn termination, and session-end reasons `channel_closed`/`shutdown` are
never continued. Subagent lifecycle remains evidence-only in Reconc because
repository TASK completion belongs to the parent session.

`reconc grok` remains the explicit strict ACP driver. It selects Grok's
`xai.api_key` authentication method when `XAI_API_KEY` is set and that method
is offered; otherwise it uses Grok's offered cached-token method. Reconc owns
the local ACP transport and policy gates, while the external Grok process owns
model authentication and inference traffic. When the installed Grok guide
documents passive Stop, optional leader mode (`grok --leader`, config
`use_leader`, or `GROK_LEADER_SOCKET`) supplies backward-compatible TUI
continuation through protocol 1 `_x.ai/interject` over Unix sockets or Windows
named pipes. Reconc suppresses the interjection only when native Stop
capability is explicitly advertised, preventing duplicate prompts without
creating a version-based enforcement gap. The 32-attempt leader cap counts
only delivered interjections in one no-progress series; material progress, a
new block, or a clean Stop resets it. Multiple endpoints divide the
three-second budget fairly and framed messages complete short writes.
`RECONC_GROK_STEER=0` disables only leader steering; PreToolUse remains hard
while native Stop remains dependent on the installed host capability. Deep
doctor reports installed native Stop capability and separately probes optional
leader protocol plus `_x.ai/interject` with a random nonexistent session.
`reconc run on|off|reset|status|log` is the canonical AI-operated repository switch.
Its durable state applies only to the selected repository, not the whole machine.
Repository mode persists across sessions for Claude Code, Codex, GitHub
Copilot, Cursor, OpenCode, Devin CLI, Antigravity CLI, Kilo Code, and Grok
Build. The agent
runs these commands itself; users do not need to operate Reconc. Prompt text,
runtime interrupts, compaction, session boundaries, runtime changes, and
application restarts never mutate the switch. An interrupt releases only the
current host invocation. `reconc run on` refuses before mutation unless live
policy sources, the compiled lockfile, and executable typed TASK state are
ready; `--force` is the explicit exceptional override. `reconc run off` is the
only normal manual disable action;
complete or absent TASK state disables it automatically after terminal gates.
`reconc run reset` is recovery-only and replaces corrupt or foreign
`state.bin` with a clean disabled state while retaining decision evidence.

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

The durable switch uses `.reconc/run/state.bin` only. Its two alternating
512-byte slots carry a fixed 88-byte payload, monotonic sequence, and CRC32C
over both header and payload. The payload includes a SHA-256 identity of the
canonical repository root. Copied state and older unbound formats fail closed
with one exact `run reset` remediation. Decoding allocates no state strings,
and a torn newest slot falls back to the previous valid slot. There is no legacy mode discriminator,
marker cleanup, or `.reconc/runloop/` compatibility read.

`awaiting_continuation` is not a hard stop reason by itself. Reads and unrelated
hook events do not clear it. Each session owns its no-progress counter and
typed-TASK-plus-material-event fingerprint in external race-safe session state,
so concurrent agents cannot reset or consume each other's budget. A bounded
material-event counter advances only for write and command outcomes, so TASK
changes or real tool progress reset that session without a Git dirty scan.
After six no-progress Stops, repository mode releases one invocation and resets
only that session without silently changing the durable switch. Strict Grok
Stops bypass the six-event guard and use the separate 32-delivered-interjection
cap. Every continuation and material transition is persisted in
`.reconc/run/decisions.jsonl` with bounded identifiers, branch, and counters,
never prompt text. The live log and two archives are each bounded at
2 MiB; readers merge the ring in chronological order.
Repeated identical policy feedback shrinks to stable `RB-*` feedback IDs,
rule IDs, and the saved report path. PreToolUse evaluates only pre-execution
write/shell rules,
generated Claude, Codex, GitHub Copilot, Cursor, Devin, Antigravity, and Grok configs do not spawn PreToolUse for
read-only matchers, authoritative PostToolUse events record evidence while
Cursor `afterShellExecution` records only passive liveness,
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
`.reconc/run/`, `.reconc/locks/`, `.reconc/reports/`, and
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
survive as orphans after a blocked hook. Workflow-audit launchers bind their
cache keys to a recursive content digest of the runner source, module files,
and generated inputs; missing or unreadable inputs fail closed. They build
cached binaries behind an atomic mkdir build lock and publish via temp binary +
rename; parallel agent hooks therefore wait for one rebuild instead of stampeding
the Go compiler or exposing a partially written cache binary. Direct audit Git
commands have a 15-second deadline; generated-reference build and execution
have a two-minute deadline, and every command has a two-second process/pipe
wait bound after cancellation.
Independent cold workflow-audit keys execute concurrently behind per-key
singleflight locks. Only short cache read/merge/atomic-publication sections are
globally serialized, so parallel results cannot overwrite each other. Runtime
retention no longer piggybacks on audit-cache publication. The task-state cache hashes only
`docs/tasks.md`, `docs/spec.md`, schema, and open TASK bodies on its hot path. A clean completed
TASK archive is represented by its committed Git tree ID plus directory
metadata; dirty or unreadable archive state bypasses caching entirely, avoiding
full archive reads without hiding archived-file changes. Cache input tree walks
now propagate traversal failures into a blocking audit result and never publish
a pass from an incomplete tree. TASK diff-aware gates likewise report staged or
working-tree Git diff failures instead of silently exempting changed completed
TASKs. Lockfile JSON readers decode directly from the existing byte slice,
avoiding one full lockfile copy per policy check and TUI summary. The in-process normal
executable-TASK benchmark on Apple M1 originally improved from 1,504,653 ns/op,
61,612 B/op, and 553 allocs/op to 131,483 ns/op, 29,225-29,276 B/op, and 245
allocs/op before continuation records became durable. With one bounded
decision-log record per continuation, the current seven-run range is
10,355,060-14,049,392 ns/op with a 10,986,170 ns/op median, 55,561-55,707 B/op,
and 457 allocs/op. The benchmark excludes process startup and Git, but includes
the state and decision-log durability writes. Reproducible Stop and concurrent-cache
benchmarks live beside their regression tests and run with
`go test ./internal/runtime/agentsession -run '^$' -bench 'RepositoryRunStopHotpath|StopPolicy' -benchmem`
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
Worktree-walking Git calls in the workflow-audit harness run under their own
25-second `repoScanCommandTimeout` instead of the 15-second short command
budget. `git clean -nd` scales with worktree size rather than index size, so the
short budget killed it at random on a large repository and reported only the
kernel's `signal: killed`. The scan budget stays below the `timeout_sec` the
invoking `require_script` rule grants, so the inner deadline fires first and an
expiry is reported as a classified scan timeout that states nothing was
verified rather than as an untracked-content violation. The message never
suggests rerunning the gate, because a gate that passes on a retry verifies
nothing.

## Publication Boundary

`make publication-audit` deterministically scans every Git-tracked working-tree
file, including release notes, tests, assets, and the complete
`harness/template/repo-root-scaffold` output. It rejects private project-name
digests, personal absolute paths, agent session/share URLs and trailers,
token/key-shaped credentials, embedded URL credentials, sensitive tracked
filenames, transcript exports, placeholder `.gitkeep` residue, unreadable or
oversized files, and non-canonical tracked paths. Forbidden private names are
stored only as one-way digests, so the scanner does not reintroduce them into
the public tree. Tests construct every negative fixture from split strings and
prove that each rule can fail.

The sole allowlist exception is history-scoped, owner-labelled, and bounded at
commit `520dd9348c1d35acb581768c8979c29fbc025c2a`. Legacy public session trailers
and pre-sanitization vocabulary at or before that commit remain untouched
because history and protected tags are not rewritten. Every descendant commit
message, changed path, and newly reachable blob is scanned for the same
private-path, private-name, session, secret, and sensitive-filename patterns as
the working tree. The audit therefore catches a leak even when a later commit
removes it. It requires full Git history; all CI and release checkouts use
`fetch-depth: 0`. This is an explicit containment boundary, not a claim that old
public history was erased.

## GitHub And Release

GitHub workflows:

- `.github/workflows/reconc-ci.yml`
- `.github/workflows/codeql.yml`
- `.github/workflows/reconc-release.yml`

CI checks:

- full root-module and `harness/template` tests on Ubuntu 24.04 and macOS 15;
  formatting, tidy, vet, pinned Govulncheck v1.6.0, pinned Staticcheck v0.7.0,
  and race checks run once on Linux
- native Windows 2025 root-module and `harness/template` tests plus native
  binary version/help smoke and native PowerShell installer success, malformed
  manifest, missing asset, checksum, execution, locked/unwritable target,
  attestation, cleanup, and existing-install preservation paths;
  shell hook wrappers and shell policy scripts use the documented `sh` runtime
- push and pull-request checks exercise the Windows installer entirely against
  the candidate binary and local fixtures, so an unpublished release candidate
  never depends on a nonexistent remote asset. After publication, a manual CI
  dispatch with `live_release: true` additionally verifies the tagged Windows
  binary and checksum manifest over HTTPS;
- SHA-pinned GitHub-owned `actions/setup-node` provisions Node.js 24.18.0 with
  implicit package-manager caching disabled. Each executable-test job packs
  exact `bun@1.3.14`, compares the tarball's npm SRI against the committed
  SHA-512 value, installs only that verified tarball, and checks the runtime
  version before executing OpenCode/Kilo adapter contracts
- every CI job that executes Go provisions the SHA-pinned `actions/setup-go`
  action from `go.mod`, including the isolated release-trust job
- clean-repository self-hosting golden path on Ubuntu and macOS across all three
  bootstrap profiles, git pre-commit, and all nine agent runtimes
- current-tree and post-boundary-history publication audit on Ubuntu, macOS,
  native Windows, release-trust, and tagged release paths
- immutable action commit pins plus an explicit GitHub-owned action allowlist;
  the trust gate validates pin shape and action identity without coupling
  updates to historical commit values
- least-privilege permissions, disabled checkout credential persistence, bounded job timeouts, and stale-run cancellation per branch or pull request
- release and installer negative-path trust tests
- manual-build Go CodeQL analysis for the root and `harness/template` modules
  on every pushed candidate, pull request against `main`, weekly schedule, and
  manual dispatch, with only `contents: read` and `security-events: write`

CI runs on candidate branches, on contributor pull requests, after accepted
updates to `main`, and through explicit manual dispatch. The pull-request
trigger only tests a pull request that somebody already opened; it never
creates one. `.github/dependabot.yml` groups security-update pull requests
separately for GitHub Actions, the root Go module, and `harness/template`.
Routine version-update pull requests remain disabled on all three surfaces,
and the repository does not enable auto-merge.

The public source repository protects its default branch with the active
`Protect main` ruleset. It blocks branch deletion and non-fast-forward updates,
and requires successful Ubuntu, macOS, native Windows, release-trust, and Go
CodeQL checks for the exact candidate commit before `main` can advance. A pull
request is not mandatory, but an unchecked direct push is rejected; maintainer
fast-forwards must first obtain the same checks on a candidate branch.
Effective rules are read back with
`gh api repos/Christopher-Schulze/reconc/rulesets/18998289`.
Repository Actions settings allow only GitHub-owned actions and require full
commit-SHA pins. Private vulnerability reporting is enabled. The active release
tag ruleset protects `reconc-v*` tags from update and deletion.

Release:

- Create the protected `reconc-vX.Y.Z` tag, then explicitly start the Release
  workflow and supply that existing tag. Tag pushes never trigger a release.
- The tag version must be stable semantic versioning, match the source version, and have committed release notes.
- Release workflow provisions the same pinned GitHub-owned Node.js runtime and
  exact verified Bun runtime, then repeats formatting, tidy, test, vet, pinned
  Govulncheck, pinned Staticcheck, race, publication, trust, and clean-repository
  self-hosting gates before building.
- `make release VERSION=<tag-version>` builds the exact flat release inventory.
- The flat inventory includes `install.sh` and `install.ps1`; both are
  checksummed, byte-compared with tagged source, and covered by the same
  provenance manifest as the binaries.
- Release output includes deterministic SPDX 2.3 and CycloneDX 1.6 SBOMs for
  both Go modules, selected dependencies, the Go toolchain, version, and commit.
- Every artifact is verified against `SHA256SUMS` before upload.
- Embedded deterministic build provenance binds every binary to its target and production-source digest; GitHub build-provenance attestations bind every manifest-listed artifact to the tagged workflow run.
- GitHub publication is rerun-safe and stays draft until every verified artifact and the manifest upload successfully.
- No Docker image is built or published.

Reproducibility basis: release binaries are cross-compiled with a pinned Go
toolchain, `-trimpath`, and `CGO_ENABLED=0`, so identical toolchain and
source produce identical binaries. `SOURCE_DATE_EPOCH` feeds the SBOM
generator and release man page; the Go compiler embeds no timestamps. There is no
independent rebuild attestation; the GitHub provenance attestation over
`SHA256SUMS` is the cryptographic binding between artifacts and the tagged
workflow run.

## Git Ignore Policy

The standalone product repository does not bootstrap Reconc into itself.
The hook registry, generators, adapters, templates, `bin/hook`, and isolated
bootstrap fixtures are committed product assets. Repo-local hook activation
files and installed wrappers belong only in target repositories; this source
root does not activate them.

Commit:

- `.gitattributes`
- `.github/releases/**`
- `.github/workflows/**`
- `.gitignore`
- `AGENTS.md`
- `assets/**`
- `bin/hook`
- `buildprovenance/**`
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
- `harness/**`
- `install.sh`
- `install.ps1`
- `internal/**`
- `schemas/**`
- `scripts/audits/**`
- `scripts/release/**`
- `scripts/tests/**`
- `skills/**`

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
- `.env`
- `.env.*` except `.env.example`
- `*.pem`
- `*.key`
- `*.p12`
- `*.pfx`
- `tmp/`
- `*.log`
- `*.prof`
- `*.pprof`
- `*.trace`
- `*.coverprofile`
- `/CHANGELOG.md`
- `/changelog.md`
- `/CHANGES.md`
- `/bench-baseline.txt`
- `/todo.md`
- `/todo/`
- `/docs/todo.md`
- `/docs/todo/`
- `/docs/tasks.md`
- `/docs/tasks/`
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
- `.reconc/run/`
- `.reconc/task-transaction.json`
- `.reconc/bootstrap-*.json`
- `*.reconc-candidate-*`
- `*.reconc-remove-candidate-*`
- `**/.reconc/policy.lock.json`
- `**/.reconc/.compile.lock`
- `**/.reconc/audit.jsonl`
- `**/.reconc/audit.jsonl.*`
- `**/.reconc/cache/`
- `**/.reconc/locks/`
- `**/.reconc/sessions/`
- `**/.reconc/reports/`
- `**/.reconc/run/`
- `**/.reconc/task-transaction.json`
- `**/.reconc/bootstrap-*.json`
- `**/*.reconc-candidate-*`
- `**/*.reconc-remove-candidate-*`

## Security

Security posture:

- Agent payloads are untrusted input.
- Hook runtime payloads are size and depth bounded.
- Paths use operating-system filesystem identity and are constrained to the
  discovered repository root, including Windows junction and 8.3 aliases.
- Payload command strings are matched as data and are not executed.
- Only policy-authored `require_script` entries execute subprocesses.
- Audit log is opt-in via `RECONC_AUDIT=1`.
- Non-portable current lockfile root markers are a hard stale/fail condition;
  equivalent clones and worktrees share the portable `.` identity.
- Current lockfiles carry a self-digest over the canonical payload, and their
  embedded rules must equal the policy parsed from current sources.

Reconc is a deterministic repository control plane, not an operating-system
sandbox. A deliberately hostile same-user process can replace local policy,
hooks, state, or binaries, fabricate self-reported evidence, or bypass a Git
hook. Strong adversarial enforcement requires an external sandbox and
protected remote CI or branch rules outside the agent's write authority.
Repository-local hook wrappers deliberately prefer development binaries during
self-hosting and otherwise select stable or unambiguous versioned local
artifacts before PATH. Those files are not a hostile same-user trust boundary.

Security reports should be private first and include the command, policy,
lockfile shape, payload if relevant, and reproduction steps.

## License

`reconc` is distributed under the MIT License.

Copyright (c) 2026 Christopher Schulze.

## Documentation Rules

`docs/documentation.md` is the current documentation SSOT.

Allowed supporting docs:

- `docs/architecture.md` for contributor architecture and threat-model detail
- `docs/commands.md` for the complete command reference
- `docs/rfcs/**` for frozen contracts
- `.github/releases/**` for historical release notes
- `assets/reconc-visual-philosophy.md` for the visual identity contract
- `CONTRIBUTING.md` for the contributor workflow
- `README.md` as the GitHub landing page
- `SECURITY.md` as security policy

Local source-planning and release-note files such as `docs/tasks.md`,
`docs/tasks/**`, `todo.md`, `docs/todo/**`, and `CHANGELOG.md` are ignored in
this repository. These source-root ignores do not apply to TASK control planes
that governed bootstrap creates in target repositories. When behavior changes,
update `docs/documentation.md` first. Generic Reconc behavior is ported into
this standalone repository before project-specific forks claim parity;
project-only workflow or policy behavior stays in its owning repository.
Supporting docs may link to this file, but should not become competing
current-state documentation.

## Release State

The current source line is `v0.8.x`; the source version is `v0.8.7`. Release
artifacts are produced only through an explicit manual Release workflow
dispatch for an existing `reconc-vX.Y.Z` tag; tag pushes never publish a
release.
