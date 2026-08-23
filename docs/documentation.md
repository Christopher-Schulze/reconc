# reconc: Repository Control Compiler Documentation

This file is the source of truth for current reconc product documentation.
RFCs may remain in `docs/` as frozen contracts, but user-facing installation,
usage, architecture, release, and security facts should be kept here first.

## Contents

- [Product](#product)
- [Evidence-Bound Completion Control](#evidence-bound-completion-control)
- [Input Bounds And Diagnostic Completeness](#input-bounds-and-diagnostic-completeness)
- [Install And Build](#install-and-build)
- [Transactional Bootstrap](#transactional-bootstrap)
- [v0.9 CLI Product Contract](#v09-cli-product-contract)
- [Daily Workflow](#daily-workflow)
- [FAQ](#faq)
- [Troubleshooting](#troubleshooting)
- [Upgrading](#upgrading)
- [v0.8.8 To v0.9.0 Migration](#v088-to-v090-migration)
- [v0.9.0 To v0.9.1 Migration](#v090-to-v091-migration)
- [v0.9.1 To v0.9.2 Migration](#v091-to-v092-migration)
- [v0.9.2 To v0.9.3 Migration](#v092-to-v093-migration)
- [v0.9.3 To v0.9.4 Migration](#v093-to-v094-migration)
- [v0.9.4 To v0.9.5 Migration](#v094-to-v095-migration)
- [v0.9.5 To v0.9.6 Migration](#v095-to-v096-migration)
- [v0.9.6 To v0.9.7 Migration](#v096-to-v097-migration)
- [Uninstall And Remove](#uninstall-and-remove)
- [Development Control Plane](#development-control-plane)
- [Minimal Example Policy](#minimal-example-policy)
- [Command Surface](#command-surface)
- [Repository Policy](#repository-policy)
- [Policy Packs And Native Assurance](#policy-packs-and-native-assurance)
- [Go-Only Action Plane](#go-only-action-plane)
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
| Premature victory claim | `reconc done .` **blocks** while policy, candidate, evidence, or typed TASK state is incomplete. | `internal/completiongate/gate_test.go:TestTypedTaskCompletionAndRequiredEvidence`; run `reconc done .` against the current candidate. | Configured checks can still omit a semantic requirement. |
| Incomplete or stubbed implementation | Opt-in `source_hygiene` assurance **detects and blocks** changed shipped source containing supported debt markers or unimplemented sentinels. | `internal/assurance/source_hygiene_test.go:TestSourceHygieneFindsHighSignalShippedCodeDebt`; run the selected assurance pack through `reconc check` or `reconc ci`. | It is a bounded lexical and language-aware gate, not proof that all implementations are substantive. |
| Skipped required work | Required reads, commands, files, claims, coupling, and assurance rules **block** when configured evidence is absent. | `internal/runtime/evaluator_test.go:TestCheckRequireCommandSuccess`; `reconc next .` returns the exact missing action. | Reconc enforces declared requirements; it does not invent the project's true acceptance criteria. |
| Workflow deviation | Installed runtime hooks and git pre-commit **block** supported events that violate compiled policy. | `internal/runtime/agentsession/handlers_test.go:TestRunPreToolUseEnforcesPolicyForbidCommandBeforeExecution`; `reconc hook status . --json` proves configured and observed routes. | Host capabilities differ, timeouts may be fail-open, and an uninstalled or bypassed hook is not enforcement. |
| Scope drift | Path, read, command, and coupling rules **constrain** the configured change surface. | `internal/runtime/evaluator_test.go:TestCheckScopedRuleOnlyFiresInsideScope`; `reconc check . --write PATH`. | Policy cannot infer unstated scope or govern actions outside the repository. |
| Protected write or deletion | `deny_write`, generated-output, secret-state, and destructive-command rules **block** at supported PreToolUse, Git, CI, or completion boundaries. | `internal/runtime/agentsession/handlers_test.go:TestRunPreToolUseBlocksApplyPatchDenyWrite`; run `reconc check . --write PATH`. | A hostile same-user process can bypass or replace local enforcement. |
| Stale or fabricated self-reported evidence | Causal write epochs, bounded session state, proof validation, and unresolved-block receipts **detect or reject** inconsistent evidence. | `internal/runtime/evaluator_test.go:TestCheckRequireCommandSuccessRequiresFreshCausalEvidence`; `internal/assurance/assurance_test.go:TestSubstantiveProofRejectsFabricatedActualAndFailedThreshold`. | Opaque claims remain only as trustworthy as their configured producer; protected external CI is required against a hostile actor. |
| Test-before-final-change laundering | Staged command receipts bind success to exact HEAD and index identity; later candidate changes **invalidate** the receipt. | `internal/commandproof/proof_test.go:TestProofBindsSuccessToCurrentStagedIndex`; `reconc exec . --staged -- COMMAND` followed by `reconc ci . --staged`. | Unstaged session evidence has a different, write-epoch-based trust boundary. |
| TASK-lifecycle bypass | Typed parsing, transition checks, recoverable transactions, and final completion checks **block** malformed or unfinished TASK state. | `internal/tasklifecycle/tasklifecycle_test.go:TestPromoteArchivesAndActivatesNext`; `reconc task validate .` and `reconc task check-done .`. | Humans and project agents still own scope, priority, acceptance, and evidence quality. |
| Cross-session context drift | `session-briefing` and repository-bound TASK/run state **constrain** reentry to current machine-readable state. | `internal/tasklifecycle/tasklifecycle_test.go:TestInspectSectionsAndBoundedBriefing`; `reconc session-briefing . --json`. | Reconc supplies bounded state, not complete conversational memory or semantic intent. |
| Inconsistent multi-agent compliance | One compiled policy and registry-backed adapters **constrain** supported agents to the same decision engine. | `internal/cli/surface_parity_test.go`; `reconc hook status . --json` distinguishes installed, configured, live, degraded, and unsupported routes. | Adapter presence is not live execution proof, and host enforcement capabilities are not identical. |
| No-progress continuation loop | Per-session material fingerprints and bounded counters **detect and release** repeated continuation without progress. | `internal/runtime/agentsession/repository_run_hotpath_test.go:TestRepoRunNoProgressGuardReleasesOneStopWithoutDisabling`; `reconc run status . --verbose`. | The guard bounds Reconc-controlled continuation; it cannot stop an independent external loop. |

The public narrative stays consistent at three depths:

1. **10 seconds:** An agent saying "done" is a claim. Reconc checks current
   repository evidence before accepting it.
2. **30 seconds:** The submitted product video shows a protected action and
   missing proof block, applies the exact remediation, runs the real test, and
   accepts `done` only after the TASK and proof agree.
3. **2 minutes:** Reconc compiles repository-owned policy, applies the same
   decision engine across CLI, hooks, Git, CI, TASK state, and run control,
   binds command success to the candidate it verified, and exports a portable
   proof bundle. It proves only that the configured repository contract and
   recorded current evidence agree, not that the model is honest or the code
   is universally correct.

Received portable proof bundles can be checked without trusting repository
scripts by running `reconc proof verify FILE`. Strict decoding rejects
oversize, non-regular, symlinked, malformed, duplicate-key, unknown-field,
missing-field, null-collection, and trailing-value inputs before semantic
verification. Optional `--repo REPO` binding compares the proof with a fresh
read-only local completion snapshot. A valid unsigned self-digest proves
integrity only; it does not identify the author or establish trusted release
provenance. Command-proof `command_hash` values are stable SHA-256 hashes of
the sanitized executable identity only. They intentionally do not commit to
the normalized command or any argument, so an observer cannot use the public
bundle as an offline argument-guessing oracle. The visible command is an
executable summary with arguments redacted; sanitization is bounded, portable,
UTF-8-safe, and defense in depth rather than a guarantee of discovering every
possible secret format.

Submitted Build Week video, Devpost text, and the immutable v0.8.6 artifacts
remain historical evidence. Current README and documentation use this
terminology; future repository descriptions, release notes, video captions, and
social posts should derive from this section instead of retroactively editing
the submission or copying a second long-form explanation.

## Input Bounds And Diagnostic Completeness

Repository and operator input is inspected before allocation. Shared readers
reject special files, enforce a byte or entry ceiling, and verify opened-file
identity. Strict surfaces reject final symlinks; the few discovery reads that
intentionally follow a symlink still require the resolved target to remain the
same regular file. FIFO, device, sparse oversize, replaced-path, unreadable, and
truncated inputs return an explicit error. Bounded directory reads return no
partial snapshot when the entry ceiling is exceeded. Streaming readers also
revalidate file identity, mode, size, and modification time after the complete
consumer callback; drift invalidates the whole result instead of exposing a
partial audit, run-log, checksum, release, or provenance snapshot.

| Surface | Current read contract |
| --- | --- |
| Policy and deep doctor sources | 8 MiB per source, 64 MiB aggregate, at most 4,096 policy sources. One rooted filesystem handle anchors each complete policy-source load; every opened file is revalidated against that root and its stable identity before and after the bounded read. Deep doctor derives freshness, parsed rules, conflicts, and references from one immutable snapshot. If full loading fails, a narrower bounded raw-reference fallback keeps source, preset, and template errors independently reportable. |
| CLI lockfiles, reports, and extraction | Lockfile summaries are capped at 16 MiB; saved `why` reports at 32 MiB; session-briefing reports at 1 MiB; `extract --from` at 8 MiB and repository-relative only. |
| Adoption and overlays | Root manifests are capped at 1 MiB, `.reconc.yml`, user presets, and user templates at 8 MiB; workflow, preset, and template directories stop at 4,096 entries and report incomplete inspection. |
| Run decisions | Each live or archived JSONL file is capped at 2 MiB and each record at 32 KiB. Two archives are retained. `run log --limit N` validates the full retained chain while keeping only the requested tail in memory. |
| Audit evidence | Each live or archived JSONL file is capped at 2 MiB, each record at 32 KiB, the detached head at 16 KiB, and the ring at two archives. Audit parent directories use `0700`; live/archive/head/lock/journal/backup members use `0600` and are identity- and security-validated before reads or writes. Existing legacy modes are migrated in place only after regular-file checks; symlinks, special files, wrong owners, and invalid lock aliases are rejected without discarding evidence. Same-process append bursts serialize per audit directory before the bounded cross-process lock; the file lock remains authoritative across processes. Export streams only after the complete chain verifies. Portable workflow-audit files are strict non-symlink regular reads capped at 64 MiB; directory walks stop at 100,000 entries, task schemas and legacy prune policies at 1 MiB, and legacy retention directories at 4,096 entries. |
| Bootstrap and repository sync | Managed text is capped at 16 MiB; bootstrap plans at 4 MiB; sync plans at 8 MiB; portable receipts at 4 MiB; rollback before-images at 64 MiB aggregate; journals at 96 MiB; binary artifacts at 256 MiB. Writes remain create-only or atomic and preserve user-owned bytes. |
| Build provenance | Binary marker inspection streams at most 256 MiB without executing or retaining the binary. Production source hashing accepts at most 16,384 real files, 64 MiB per file, and 512 MiB aggregate. |
| Command proofs and owned state | Each command proof is capped at 16 KiB and its directory at 4,096 entries; unresolved-policy proofs and workflow-audit cache state are capped at 8 MiB. All are strict regular-file reads that reject links and special files. Retention directories and tree walks have explicit entry ceilings and abort without deleting from a partial inventory. |
| Policy script execution | A `require_script` target resolves inside the repository before launch, `timeout_sec` is capped at 300 seconds, and `kill_timeout_sec` at 60 seconds. Captured stdout and stderr stop at 64 KiB per stream. |
| Impact Lab | Candidate policy files are capped at 8 MiB; strict replay corpora and full typed JSON reports at 64 MiB, corpora at 10,000 cases, and reviewed action-delta manifests at 8 MiB. JUnit, SARIF, and GitHub projections retain at most 1,024 findings and 8 MiB. |
| Action approvals and state | Canonical approval objects are capped at 64 KiB, authority registries at 1 MiB, sealed request state at 4 KiB, approval TTL at 120 seconds, future issuance skew at 30 seconds, pending approvals at four, retained approval records at 65,536, and the complete private action state at 16 MiB. |
| Action content inspection | Canonical action values are capped at 8 MiB, strings at 4 MiB, nesting at 32, and JSON items at 65,536. Output schemas are capped at 1 MiB and 8,192 items, MCP results at 4,096 content blocks, decoded binary blocks at 3 MiB, and inspection at 500 ms pre-call, 1 second post-result, or 250 ms progress. |
| Action decision ledger | Each typed payload-free record is capped at 64 KiB, the live file and each of two archives at 4 MiB, the detached head at 8 KiB, and the authenticated incremental checkpoint at 16 MiB. Appends use a ten-second private cross-process transaction boundary; queries return records only after the retained chain, archives, and detached head verify. |
| MCP gateway | Protocol frames and results are capped at 10 MiB, arguments at 8 MiB, discovery at 512 tools across 64 pages and 8 MiB aggregate metadata, concurrent calls and pending approvals at four each, progress at 128 events and 1 MiB per call, retained child stderr at 256 KiB, and serialized operator diagnostics at 4 KiB per line. One Go 1.27 `jsontext` pass validates framing and extracts borrowed envelope fields for observers and progress routing before the SDK consumes the original bytes. Escaping fields are cloned, transformed bytes alone are revalidated, and cleared reader/writer buffers are retained only through 256 KiB. Discovery charges and validates each page before requesting the next, retains only canonical contracts, and therefore holds at most the aggregate catalog plus one bounded in-flight page instead of all raw pages. Every observed SDK call consumes its pending or request-ID state on completion, cancellation, timeout, or response failure, so no terminal path retains the serialized send lease. Tool icons are limited to 32 fully decoded self-contained PNG or JPEG data URIs, 48 KiB each, 2,048 pixels per side, and 4,194,304 pixels; remote URLs and decompression bombs fail closed. Tool `_meta` is absent or empty because extension semantics are not enforced. Definitions, frames, progress, stderr, and results are validated or inspected before exposure. |
| Auxiliary commands and release inventory | Git, Go, attestation, offline-hook, TASK utility, generated-reference, SBOM, and publication-audit subprocesses use purpose-specific 64 KiB to 64 MiB output ceilings and fail on overflow. Release assets are hashed as stable non-symlink regular-file streams, release directories stop after the declared inventory ceiling, and committed manifests, archives, and SBOMs use strict bounded reads. |

Best-effort detection is best-effort only about recommendations, not about
input integrity. A malformed package manifest, unreadable source, oversized
report, or overfull workflow directory is surfaced as an ambiguity or error and
is never converted into “not present.”

## Install And Build

Requirements:

- Go `1.27`
- macOS `13` Ventura or later for native macOS builds; this is the minimum
  supported by the Go 1.27 toolchain
- Git for `reconc ci` and hook installation
- Bun `1.3.14` for executable OpenCode, Kilo Code, Oh My Pi, and Pi adapter tests
  only; the shipped Reconc binary has no Bun runtime dependency
- Python `3.13.14` only for the pinned disposable LangChain MCP interoperability
  job; the shipped binary, product code, and core tests have no Python runtime
  dependency
- On Windows, `sh` on `PATH` for generated shell hook wrappers plus `.sh` and
  extensionless policy scripts; Git for Windows supplies it. Native `.exe` and
  `.com` policy scripts execute directly.

Common commands:

```bash
make test-fast
make test
make test-langchain
make fuzz
make vet
make lint
make coverage
make build
make benchmark-record
make benchmark-compare
go run ./cmd/reconc --help
make self-host
make publication-audit
```

The canonical Make targets cover both the root Go module and
`harness/template`. `make test-fast` rejects unformatted non-ignored Go sources
and runs cached normal tests in both modules for a short edit-feedback loop.
`make test` runs the real-repository publication audit once, then runs uncached
race suites in both modules and the release-trust failure-path checks. Both
targets cap package-level parallelism at two by default so an 8 GB development
machine remains responsive; `TEST_PARALLELISM` accepts a different positive
integer for larger hosts. The publication CLI contract test uses a
bounded temporary Git fixture instead of rescanning the real repository under
the race detector. `make test-langchain` is the separate hash-pinned external
consumer proof. The `LangChain MCP interoperability` check is required for
protected `main`, and the Release workflow reruns the same proof against the
exact selected tag before publication. `make fuzz` uses 500 executions and one worker per target
by default, avoiding the Go 1.26 time-deadline shutdown race while keeping the
gate deterministic and bounded. Minimization is likewise bounded to ten exact
executions, and a disposable per-run Go fuzz cache prevents machine-local corpus
history from consuming or changing that fixed budget. `FUZZ_TIME`,
`FUZZ_MINIMIZE`, and `FUZZ_PARALLEL` explicitly change those budgets and the
worker count. Direct `go test ./...` validates only the root module.

`make benchmark-record` runs the calibrated performance-history suite five
times at a fixed iteration count and writes the machine-local result under
`.build/benchmarks/`. `make benchmark-compare` normalizes every target against
its same-package calibration benchmark before comparing it with the checked
baseline. The suite covers twelve groups: bounded action traces and context
operands, prepared action-decision caching, incremental action-ledger checkpoints, structured action inspection,
canonical JSON, contextual source ingestion, prospective path resolution,
prepared command matching and evidence, source freshness, write-epoch batching,
and bounded hook-worker frame growth. Its parser
reconstructs benchmark lines split across Go JSON output events. `make
benchmark-baseline` is the only baseline-writing operation and requires
`CONFIRM_BENCHMARK_BASELINE=1`.

Apple M1 TASK-269 before/after checks reduced deep-doctor allocation volume by
about 10.8% and allocations by about 16.3%. Multi-platform hook status reduced
allocation volume by about 30.8% and allocations by about 14.8%; filesystem
wall time remained noisy. A 1 MiB hook-worker frame fell from about 4.76 MiB
and 13 allocations to about 1.39 MiB and 4 allocations, with lower measured
latency. The checked baseline records the worker result through same-package
calibration rather than absolute timing.

Go 1.27 synthetic time is restricted to tests whose complete dependency graph
is in memory. The audit append gate tests use `testing/synctest` to prove
serialization, cancellation, cleanup, and recovery without wall-clock waits;
Git, subprocess, filesystem, and file-lock tests retain real time. The MCP
gateway refresh-worker shutdown regression queries the runtime
`goroutineleak` profile for its exact worker stack only. It neither requires a
globally empty profile nor treats unrelated test-process goroutines as product
leaks.

Custom-runtime host normalization uses Go 1.27's stable
`encoding/json/jsontext` decoder in two bounded streaming phases: one strict
whole-document validation pass and one route-pointer trie pass. The selector
walks shared pointer ancestors once, uses `SkipValue` for unselected subtrees,
and decodes a selected subtree into `json.Number`-preserving Go values only
after its raw byte span fits the retained-value budget. It never builds a
generic representation of the complete host object.

The checked Apple M1 benchmark (`-benchtime=5x`) measured the streaming path at
301,792 ns and 284,515 allocated bytes for a 64 KiB typical payload, versus
592,175 ns and 412,702 bytes for the former interface-tree reference. At the
8 MiB boundary it measured 14,545,458 ns and 33,584,300 bytes versus 29,269,683
ns and 50,358,718 bytes. The 256-byte case was 43,867 ns versus 34,542 ns; the
accepted tradeoff is a small fixed strict-streaming cost while typical and
maximum payloads roughly halve latency and materially reduce allocation volume.

Make targets:

```bash
make build
make test-fast
make test
make test-langchain
make fuzz
make vet
make lint
make coverage
make cover
make bench
make benchmark-record
make benchmark-compare
make benchmark-baseline
make self-host
make publication-audit
make sbom VERSION=0.9.7
make notices VERSION=0.9.7
make release VERSION=0.9.7
```

`make coverage` runs both Go modules with atomic whole-module instrumentation
(`-coverpkg=./...`) and reports the measurements for review only. The profiles
are written to `coverage.out` and
`harness/template/coverage.out`. `make cover` records the same measurements and
also writes separate HTML reports beside those profiles. Coverage uses the same
bounded `TEST_PARALLELISM` setting as the test targets. Meaningful tests must
exercise changed behavior, while OS-specific files and process entry points
still require their matching platform jobs or integration boundaries.

`make release` cross-compiles five binaries into `dist/`, copies the native
POSIX and Windows installers, generates three flat shell-completion artifacts,
generates a man page, copies all 36 independently versioned schemas from the
typed registry under unique current or legacy release names, and generates
deterministic SPDX 2.3 and CycloneDX
1.6 SBOMs, copies the project license, generates deterministic exact
third-party notices from the binary dependency graph for all five release
targets,
generates a strict `release-manifest.json`, and writes `dist/SHA256SUMS`. The
target stops on the first build, license, SBOM, manifest, or checksum failure.

Each copied artifact has one owner. `internal/schema` owns schema source paths,
release names, immutable URLs, and digests; `scripts/release/copied-assets.tsv`
owns the four non-schema source copies. The build and verifier consume both
inventories directly. Generated surfaces and target-derived binary names are
owned once by `scripts/release/generated-assets.sh`; the Makefile generates
from that executable inventory and the verifier lists from it.

The release verifier requires exactly those fifty-three checksummed artifacts,
rejects missing, extra, duplicate, unsafe, mutable, or corrupted entries, and
never accepts an empty manifest. It independently verifies every manifest
asset and digest, then regenerates Bash, Zsh, Fish, and the versioned man page
from the current canonical command metadata and byte-compares every released
schema, installer, and harness pack with its canonical source. Even freshly
checksummed artifacts whose bytes are stale or noncanonical fail. `dist/` is
ignored and should not be committed.

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

The release notice generator unions the exact package dependency graphs of all
five `CGO_ENABLED=0` release targets, rejects module replacements and missing or
symlinked root license files, includes the Go toolchain license and patent
notice, and preserves each source text with its SHA-256 digest. Verification
regenerates `THIRD_PARTY_NOTICES.txt` from the tagged source and byte-compares
it before checksums and provenance are accepted.

`install.sh [--channel stable|preview | --version VERSION]
[--allow-downgrade]` supports macOS and Linux. `install.ps1 [-Channel
Stable|Preview | -Version VERSION] [-AllowDowngrade]` supports Windows x64.
Omitting a selector resolves the latest stable GitHub release; an exact version
uses only its immutable tag. Preview selection never accepts a draft or stable
release. Exact downgrades fail unless the caller explicitly opts in. A custom
release base requires an exact version and cannot be mixed with channel
discovery. POSIX defaults to `~/.local/bin`; Windows defaults to the
user-writable `%LOCALAPPDATA%\Programs\Reconc\bin`. Both report the exact PATH
remediation when bare `reconc` does not resolve to the installed binary instead
of elevating or silently editing shell configuration. Both installers
download the exact platform binary and published `SHA256SUMS` over HTTPS,
bound metadata to 2 MiB and binaries to 256 MiB, require exactly one matching
hexadecimal SHA-256 entry, verify the payload
before executing it, and delegate binary publication to the candidate's
`install-cli` transaction. Candidate download, local source installation,
private backup, atomic publication, checksum verification, and rollback use
bounded files and fixed 128 KiB buffers; the transaction never retains complete
old and new binaries as byte slices. Candidate and backup files are private,
identity-checked, synced, and removed on every resolved path. A failed restore
retains the only verified backup and reports its exact recovery path. When the
installed binary is current on PATH, that
same locked transaction writes a private, checksum-bound ownership receipt at
`$RECONC_HOME/install/receipt.json`. An off-PATH invocation can publish the
verified binary, but exits non-zero with exact PATH remediation and does not
claim ownership. Failures before candidate execution leave the previous valid
binary and receipt untouched. Every non-zero `install-cli` result fails the
outer installer too; the error distinguishes a retained or restored target from
matching candidate bytes already published as an exact recoverable partial
state. Windows arm64 remains
unsupported until the release matrix ships a matching native asset.

The v0.9 platform contract is one matrix:

| Platform | Direct installer | Architectures | Ownership |
| --- | --- | --- | --- |
| macOS | `install.sh` | amd64, arm64 | `direct` |
| Linux | `install.sh` | amd64, arm64 | `direct` |
| Windows | `install.ps1` | amd64 only | `direct` |

Direct installers own only the verified binary and receipt. No path silently
edits a shell profile or global environment.

The immutable v0.9.6 tag contains both `install.sh` and `install.ps1`. Public
bootstrap commands fetch the appropriate script from that tag, never from
mutable `main`, and install the matching checksummed v0.9.6 binary.

Both native installers require GitHub CLI (`gh`) and verify the downloaded
binary against its GitHub build-provenance attestation before execution or
publication. Verification binds the candidate bytes and digest to the fixed
`Christopher-Schulze/reconc` repository, tagged release source ref, release
workflow, and GitHub-hosted runner. Missing tooling, unavailable verification,
or a failed attestation is fatal; checksum verification from the same release
origin is not accepted as an independent downgrade.

## Transactional Bootstrap

New repositories use canonical non-interactive init:

```bash
reconc init . --profile governed --hook codex --json
```

Init installs and verifies the exact running user CLI, inspects, selects,
plans, applies, records, and verifies through `internal/bootstrap`. A fresh
repository defaults to `minimal`; one valid recorded transaction reuses its
selection. Partial, mature, ambiguous, or already governed repositories
without a receipt require an explicit profile and receive no repository write.
Existing content is never overwritten. Drift produces hash-addressed
candidates; only explicit `--accept-managed-blocks` may promote a
checksum-verified marker-only candidate. Human and stable JSON output derive
from one typed result and end in one next action.

The transparent lower-level interface remains
`bootstrap inspect|profiles|plan|apply|verify|remove`. Inspection is read-only
and reports stack evidence, package managers,
repository markers, same-directory package-manager ambiguity, and detected or
installed platform truth. Planning is read-only unless an explicit output path
is supplied. The `minimal` profile owns policy, a managed AI-orientation block,
and runtime ignores. The `governed` profile adds the TASK control plane,
documentation, `start.md`, and the stable repo-local hook wrapper. Both use
`default` and `agent` as profile defaults. Stack and platform detection produce
suggestions only; packs and hooks remain explicit.

Repository bootstrap intentionally excludes user-global integrations. Kimi
Code owns hooks only in `$KIMI_CODE_HOME/config.toml` (default
`~/.kimi-code/config.toml`), so it is never auto-detected, selected by
`--hook all`, written by `init`, or included in repository scaffold sync. Its
separate opt-in lifecycle is `reconc hook install kimi-code` and
`reconc hook uninstall kimi-code`, both without a repository argument.

The `advanced` profile adds the complete embedded public harness pack under
`tools/reconc/harness/template/`. Its strict
`reconc.harness-pack/v1` manifest records product compatibility, capabilities,
canonical paths, modes, sizes, SHA-256 identities, ownership, total bytes, and
one pack digest. The same deterministic
`reconc-harness-pack-advanced-1.0.0.zip` is embedded in every platform binary
and shipped in the checksummed, provenance-attested release inventory.
Bootstrap plans and private install receipts bind `advanced@1.0.0` plus its
digest. Its scaffolded policy declares `cache_inputs` for every workflow gate
whose audit reads a narrow input set, and deliberately declares nothing for the
gates that walk a broad surface, select input paths dynamically, or shell out:
a partial declaration there would
re-enable Stop report reuse across state the gate actually inspects, so those
gates run whenever a write reaches them. The portable audit's separate result
cache derives stack-configured build and durable-store paths from the same
normalized helpers as the audits, verifies its input fingerprint again after
evaluation, and refuses to publish a pass if those inputs move concurrently.
Configured path expansion replaces only the exact `{project}` token, while
generated-reference execution uses the same flat-root or `codebase/` layout
resolver as other stack paths. Architecture imports are mapped only at complete
configured project path boundaries, and added-line Go comment checks distinguish
real line comments from `//` inside interpreted strings, raw strings, and rune
literals.
Loading rejects unknown fields, incompatible versions, traversal,
absolute or duplicate paths, links, unsupported modes, oversized input,
unmanifested files, and checksum or digest drift. Daily init performs no
network request and requires no standalone source checkout.

A release installer establishes the stable user CLI. A portable source build
or copied toolkit establishes the same contract without a download:

```bash
go build -o .build/bin/reconc ./cmd/reconc
.build/bin/reconc install-cli
reconc --version
```

`install-cli` defaults to `$RECONC_INSTALL_DIR`, then `~/.local/bin` on POSIX
or `%LOCALAPPDATA%\Programs\Reconc\bin` on Windows. It atomically installs the
exact running executable, rejects a symlink target, verifies its checksum and
executable mode, and verifies the executable resolved by bare `reconc`. It
never edits a shell profile or the parent process environment; when activation
or ordering is incomplete, it exits non-zero with the exact durable PATH
remediation.

Global installation state is separate from repository bootstrap state.
`$RECONC_HOME/install/receipt.json` is a strict-decoding, bounded 64 KiB
non-symlink regular file, self-digested, written with private permissions, and serialized by
`$RECONC_HOME/install/receipt.lock`. It records `direct` or `source`
ownership, channel, exact version, artifact checksum, canonical
binary identity, build target, source digest, provenance state, and canonical
UTC installation time. Native installers and explicit source `install-cli`
calls publish it only after checksum, executable, version, and PATH identity
pass. `install-cli` cannot claim an unsupported ownership type.
The lock inode is persistent once created: update, install, uninstall, and an
existing-state global diagnosis all coordinate through that same identity.
State purge validates the complete recognized inventory but deliberately
retains the private lock and its directory, preventing a concurrent process
from locking a replacement inode. Diagnosis opens an existing lock without
creating, repairing, chmodding, or rewriting state; a machine with no
installation state remains untouched. If the lock appears during an initially
unlocked read, Reconc acquires it only to revalidate the observed receipt
generation; the diagnostic operation itself executes exactly once.

Private state directories and locks are created through the shared
`internal/privatefs` boundary. It rejects symlink, irregular, wrong-owner, and
unexpected hard-link objects. Unix applies and validates private modes through
opened descriptors. Windows first binds a no-follow descriptor, applies the
protected current-user-only DACL through the supported named filesystem
security operation, then validates that DACL through the opened handle and
revalidates path identity before returning. Legacy
private directories may be repaired only at their intended boundary. Receipt,
retention, command-proof, policy-proof, and action-state paths retain their
existing locations, names, retention behavior, and JSON contracts.
The portable legacy pruner canonicalizes the existing repository filesystem
identity before deriving both the runtime-compatible project key and the
repo-local JSONL path, so symlink and on-disk case aliases cannot split cleanup
across different identities.

`reconc doctor --global [--json] [--output PATH]` is the read-only authority
for that state. It independently inspects the running executable, bare PATH
resolution, canonical target, additional PATH candidates, receipt checksum,
and embedded build provenance. Candidates are collected in the order a shell
resolves them, directory by directory, and on Windows across every name
PATHEXT makes executable, so a `reconc.bat` or `reconc.cmd` ahead of the
installed `reconc.exe` is reported as a shadow instead of being missed.
Unreadable or broken candidates are retained as warnings while later usable
candidates remain visible where platform command resolution would continue.
Target and resolved-binary checksum failures are explicit structured failures,
never aliases for `current=false`. It reports `healthy`,
`unowned`, `stale`, `shadowed`, `ambiguous`, or `invalid` plus exactly one
ownership-aware remediation. Malformed receipts, checksum drift, conflicting
owners, and ambiguous legacy installations fail closed. The JSON contracts are
`schemas/v1/installation-receipt.schema.json` and
`schemas/v1/global-diagnostic.schema.json`. Mutating update and uninstall
results use `schemas/v1/global-lifecycle.schema.json`; release discovery is
bound by `schemas/v1/release-manifest.schema.json`. All four ship in the
checksummed release inventory.

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
Repository detection accepts either a real `.git` directory or a bounded,
strict `gitdir:` metadata file whose resolved target is a real directory, as
used by linked worktrees. Missing metadata means non-Git; symlinked, malformed,
or non-directory metadata fails closed. Bootstrap and repository sync use this
single identity contract.

Apply publishes only absent targets. Exact artifacts remain unchanged. A
different file, directory, symlink, or special target produces a
hash-addressed `.reconc-candidate-*` artifact and no normal target is installed.
A mutating compatibility or transactional bootstrap first atomically installs
the exact running build as the stable user CLI, proves that bare `reconc`
resolves to it, and otherwise fails before any repository write. A stale plan
fails before publication. New files are staged beside the target,
synced, checksum-verified, and published without replacement. Publication
retains an open descriptor for the exact created inode through mode setting,
checksum verification, and transaction ownership capture. Rollback reuses
that descriptor and an opened parent identity, revalidates both before rooted
removal, and preserves any externally replaced target. Hard-link publication
and the exclusive-copy fallback share the same descriptor and cleanup
contract. A retry under the same repository transaction lock recovers only the
exact reserved stage for the same plan and target when its regular-file
identity and complete content digest match. If that stage proves an already
published exact target, the retry adopts its open identity and completes the
transaction. Changed, foreign, similarly named, linked, or otherwise ambiguous
residue is preserved with manual recovery guidance. On failure, rollback
removes only transaction-owned files whose
file identity and checksum still match, plus transaction-created directories
that are still empty.
Verification is read-only and checks artifacts, lockfile freshness, selected
hooks, governed TASK state, selected binary resolution, and the exact running
user CLI on PATH.

Successful apply records the exact durable plan, writes a private rollback
receipt, and publishes the deterministic, committable
`.reconc/install.lock.json` portable ownership receipt. The portable receipt
binds the product, policy and harness packs, hooks, policy sources, exact
managed files and blocks, generated artifacts, explicit user-owned paths,
applied plan, generation, and its own digest without a physical checkout path.
Apply reports
created/preserved/drifted/skipped and installed/configured/live counts, and
prints exactly one next command. A stale saved plan prints the exact
selection-preserving replan command with `--replace-output`.
The private receipt directory retains the current bootstrap plan/receipt pair
plus the two newest independently validated historical pairs. Cleanup removes
only strict, digest-bound, non-symlink Reconc pairs and preserves foreign,
malformed, partial, current, and linked entries. A legacy harness receipt is
importable only when its recorded pack digest matches an authenticated embedded
pack; Reconc never invents compatibility bounds for an unknown digest.

Binary installation has no network path. `--install-binary` uses the running
executable; `--binary PATH --checksum SHA256` accepts an explicit local artifact
and optional `--platform OS/ARCH`. Installed artifacts use the stable
`reconc-<os>-<arch>[.exe]` name. Resolution prefers the stable name, permits
exactly one compatible versioned fallback per searched directory, and fails on
ambiguity before trying development binaries or PATH.

`reconc bootstrap remove --plan PATH` treats the portable receipt as the
maximum ownership authority. It removes exact owned files and generated
artifacts, strips exact marker-owned blocks while preserving every outside
byte, and never deletes user-owned policy, docs, TASKs, or unrelated files
merely because an older private receipt once created them. Drift blocks the
normal mutation set and emits hash-addressed review candidates. The private
receipt may remove only its own exact lifecycle records.
`reconc hook uninstall KIND .` removes one separately selected platform with
the same ownership discipline.

Repository upgrade is a separate explicit transaction:

```bash
reconc repo sync plan . --output /tmp/reconc-sync.json
reconc repo sync apply --plan /tmp/reconc-sync.json --digest <plan-digest>
reconc repo sync verify .
```

Planning reads the portable receipt, current owned bytes, managed blocks,
generated policy lock, optional Git snapshot, and immutable packs embedded in
the running binary. It publishes no repository, Git object-database, or
persistent Reconc state unless `--output` is supplied. Ambient `GIT_*`
variables are removed, system and global Git config are disabled, repository
hooks and filesystem monitors are overridden, optional locks are forbidden,
and `git write-tree` uses a private temporary object database with the
repository object database as a read-only alternate. Repository-local identity
config remains available. Two matching snapshots are required. The plan
reports current and target policy/harness pack identities and classifies every path as `unchanged`,
`replace-owned`, `update-managed-block`, `create-owned`, `user-drift`,
`orphaned-legacy`, `incompatible`, or `manual-review`, and performs no
repository write unless `--output` is supplied.

Apply requires the exact saved digest, re-plans under the shared repository
transaction lock, refuses any stale or review state, mutates only the three
safe owned states, and advances the receipt once. Receipt-owned generated
policy is compiled from current registered sources in memory, so a missing or
historical lock can be rebuilt without a circular dependency on the old file.
Registered migrations remain explanatory evidence only when their exact output
equals the newly compiled target.
An unchanged receipt-owned binary retains its exact `binary@version`
component, checksum, mode, and file ownership when the receipt advances; sync
does not silently erase the approved binary provenance.

Before the first target write, apply publishes the bounded strict
`.reconc/repository-sync-transaction.json` journal with plan/product identity,
before-image bytes and modes, after hashes and modes, created-path state,
created parent paths, and a self-digest. All files and the receipt must pass
complete pack, binary, policy, hook, and ownership verification before the
journal is removed. Normal failures restore only exact transaction
after-images. An interrupted process leaves the journal and blocks init,
direct bootstrap apply, sync plan/apply/resolve/verify, and removal until:

```bash
reconc repo sync recover .
```

Recovery returns `clean`, `finalized`, `rolled-back`, or `refused`. A complete
after-image is verified and finalized; an exact before-image or before/after
mixture is rolled back. A path with an external edit, unexpected type, digest,
or mode is never overwritten and leaves the journal intact. Empty
transaction-created parent directories are preserved because their identity
cannot be proven after a crash.

Every non-mutable action has a digest-bound resolution:

```bash
reconc repo sync resolve --plan PATH --digest SHA256 --path RELATIVE --strategy keep-current
reconc repo sync resolve --plan PATH --digest SHA256 --path RELATIVE --strategy use-target
reconc repo sync resolve --plan PATH --digest SHA256 --path RELATIVE \
  --strategy use-binary --binary FILE --checksum SHA256 --platform OS/ARCH
```

`keep-current` preserves the bytes and releases the matching file, hook, or
harness component to user ownership. It cannot retain an invalid generated
policy lock. `use-target` adopts the exact in-memory target already bound by the
plan. `use-binary` is restricted to a receipt-owned cross-platform binary and
requires one bounded regular local artifact whose checksum and stable platform
name match. Same-platform binaries advance from the exact running executable.
A successful resolution does not pretend the full upgrade finished; it returns
one fresh-plan command. A valid single v0.8.x private plan and receipt can be
imported as bounded migration evidence but cannot grant ownership beyond the
portable receipt. Core synchronization performs no network request.

The detailed AI tutorial and project-specific advanced harness path remain in
`tools/reconc/harness/template/BOOTSTRAP.md` after advanced init.

## v0.9 CLI Product Contract

The implementation contract for the next minor release is
[RECONC-0007](rfcs/RECONC-0007-cli-product-lifecycle.md). It defines one
ownership-aware global CLI, `init` as the canonical transactional onboarding
entrypoint, durable installation and repository ownership, stable/preview/exact
release selection, an embedded advanced public harness pack, explicit binary
update and uninstall, and digest-bound repository synchronization.

RECONC-0007 is frozen with v0.9.0. Ownership-aware installation, global
diagnostics, update, uninstall,
strict release manifests, canonical init, embedded harness packs,
digest-bound repository synchronization, portable ownership receipts,
cross-platform hardening, and release gates implement one published contract.

The locked target keeps two boundaries explicit:

- direct binary update and repository sync are separate transactions;
- repository mutation is plan-, receipt-, precondition-, and digest-bound, and
  never silently rewrites user-owned policy or documentation.

## Daily Workflow

Onboard a target repository once:

```bash
reconc --version
reconc init .
```

Then use the canonical daily loop:

```bash
reconc session-briefing . --json
reconc check . --write path/to/file
reconc check . --write path/to/file --format sarif --output reconc.sarif
reconc next .
reconc done .
```

`session-briefing --json` is the bounded machine handshake for session entry
and reentry. Its versioned compact contract combines current TASK/Sub-Task,
policy delta, required evidence, exact remediation, and durable repository-run
state without Git or writes. Static reference material stays on demand through
`reconc agent-intro --section NAME` instead of inflating every agent prompt.

Review candidate policy before changing the live contract:

```bash
reconc impact . --candidate candidate.yml --write src/main.go
reconc impact export . --session --complete write,command,command_outcome --output impact-corpus.json
reconc impact . --pack go-assurance --corpus impact-corpus.json --json --output impact-report.json
reconc impact . --candidate candidate-actions.yml --corpus action-corpus.json --format github
reconc impact . --candidate candidate-actions.yml --corpus action-corpus.json --delta-manifest reviewed-deltas.json
reconc policy author . --candidate candidate.yml --write src/main.go
reconc policy author . --detected --json
reconc policy author . --candidate candidate.yml --apply
```

`impact` adds one candidate policy file or resolved pack to the current source
bundle in memory, compiles it through the production parser and compiler, and
evaluates current and candidate typed plans through the production matcher.
It never publishes a lockfile, applies suggestions, edits `extends`, changes
hooks, sessions, audit state, or TASKs, calls a model, or opens a network
connection. Policies containing `require_script` are refused before any case
is evaluated because executing an arbitrary repository script cannot satisfy
the command's side-effect-free contract.

`policy author` turns that review path into one explicit authoring transaction.
It validates a candidate first against the embedded current policy-config JSON
Schema and then against the real loader, preset/template expansion, parser,
compiler, conflict detector, and runtime lock validator. Its explanation
contains normalized rules, effective packs, body-free source provenance,
affected rule kinds, warnings, conflicts, and an Impact Lab delta only when
bounded replay evidence is supplied. Candidate bytes and physical repository
paths are excluded from its versioned JSON report.

Preview is always read-only. Non-terminal and JSON invocations never prompt;
text terminals default to no, and automation must supply `--apply` to mutate.
The selected target is restricted to one direct repository-owned
`policies/*.yml` or `policies/*.yaml` path. Apply revalidates the preview under
the canonical repository transaction lock, publishes the target atomically,
requires the production compiler to emit the exact predicted lockfile, runs a
fresh runtime validation, and rolls back its own target and lock publication on
failure. Detected pack suggestions remain review-only and never edit
`extends`.

Inputs can be explicit fixtures or strict imported corpora. Export retains
only normalized read/write paths, commands, authoritative command outcomes,
causal epochs, and claims. It stores no prompts, file bodies, command output,
raw session identifier, environment snapshot, or physical candidate path.
Secret-shaped assignments, flags, Bearer values, common provider tokens,
credentialed URLs, and secret query parameters are replaced with
`<redacted>`; every affected event class becomes incomplete. Imported files
are bounded regular non-symlinks with duplicate-key, unknown-field,
null-collection, ordering, canonical-path, self-identity, and trailing-value
checks.

The deterministic result reports per-case decision and remediation changes,
newly blocking and warning rules, resolved violations, per-rule match counts,
rules unmatched in the corpus, and a structural evaluation-cost delta. Cost
units count rules, evidence items, matcher opportunities, and external-rule
boundaries, not wall-clock time. Completeness declares which event classes the
capture covered and which were missing or redacted. Even a complete declared
replay describes only its bounded corpus; an unmatched rule is never called
dead or safe.

Format-2 corpora preserve format-1 repository replay through deterministic
migration and add strict `action_pre` and `action_post` cases. An action case
binds the server label and fingerprint, tool and tool-contract digest, exact
sanitized phase payload, trusted context and provenance, principal and credential
labels, evaluator state, completeness, and an exact expected current outcome.
The current expectation includes decision, stable reason, tool ID, ordered
matched rule IDs, cache result, completeness, phase outcome, and failure code.
An optional approval assertion additionally binds the exact approval status,
its redacted identity, the exact approval transition when one exists, and the
call-specific required-approval SHA-256 identity. Coverage records evaluator
approval snapshots and approval transitions as separate exact dimensions.
The ledger assertion binds recording mode, the phase-derived `pre_decision` or
`result_inspection` event, required-recording state, tool-identity mode, and the
exact canonical selected-field declarations. It contains declarations only,
never selected values. Ledger-policy changes are reported as their own exact
delta.
Reconc runs the current and additive candidate action plans through the
production compiler, runtime plan, normalizer, and evaluator. It never
dispatches the declared tool.

Use explicit scenarios for each security boundary. A dangerous database case
can pass `{"target":"production"}` and expect `block`; a bulk-delete case can
pass `{"operation":"bulk-delete"}` and expect `warn`; a credential-scoped
case records only a safe label such as `database-writer` and proves that label
is included in exact identity resampling; an untrusted-context spoof marks
`environment=test` as `agent_supplied` and expects `context_untrusted`; an
approval case expects `require_approval`; malformed duplicate-key and payloads
larger than the action argument limit expect exact fail-closed reason codes.
Post-result cases use the same contract for delivery or withholding. The full
portable JSON shape is committed at
`harness/template/audits/testdata/action-impact/corpus.json`.

Action payloads must be synthetic and minimized; they are not a capture format
for live arguments or complete tool results. Export removes recognized
secret-shaped values, physical paths, oversized scalars, and unsafe metadata,
and replaces an over-limit payload with one canonical safe surrogate that
still exercises the production `limit_exceeded` path without retaining source
bytes. The format-2 inspection extension carries exact payload-free detector,
schema, selected-field, unsupported-content, and containment evidence. Export
uses the same deterministic detector pack to redact recognized secret and PII
shapes, but it cannot infer confidentiality from an otherwise ordinary opaque
value. Authors must never seed scenarios with live sensitive data.

Action comparison separates every decision change and newly allowed, warned,
approval-required, and blocked changes from reason, rule-trace, cache, phase,
completeness, tool-identity, approval-state, and failure deltas. Approval-state
comparison includes status, redacted identity, and required-approval identity.
It also compares the explicit pending, approved, rejected, expired, cancelled,
unavailable, malformed, or replayed transition when present.
`newly_allowed` describes any less-restrictive decision, while `newly_blocked`
describes only a candidate decision that became an exact block. Eligible,
non-dispatchable, withheld, suppressed, and recorded phase-outcome changes are
reported independently and never reclassify warnings or approval requirements
as blocks. Therefore
`block -> require_approval`, `block -> warn`, and `warn -> allow` cannot
bypass review. Newly allowed or newly blocked cases exit 2 until an
exact reviewed delta manifest binds the case identity, current and candidate
outcomes, candidate lock digest, rationale, and either canonical UTC expiry or
permanent status. Duplicate, wildcard, orphaned, partial, stale, expired, or
digest-mismatched review fails. Compact text and full typed JSON retain stable
case IDs; JUnit, SARIF, and GitHub use the bounded CI projection. Selected
values are removed before replay and retained only as category, source,
pointer, size, provenance, and optional trusted identity. Raw credentials,
headers, tokens, physical paths, and complete results never enter the report.
The manifest binds acknowledged content but carries no reviewer-authentication
claim. Protected review or signed-commit policy must supply reviewer identity
and separation of duties.

`reconc next [PATH]` loads the latest persisted blocking decision for the
explicit or normally discovered repository. Stale decision state fails with
an exact replay remediation. When no blocking decision exists, it succeeds
with the explicit clear state `No remediation needed.` or
`{"state":"clear","remediation":null}`.

`status`, `doctor`, `sources`, `repo sync plan`, `repo sync verify`, `check`,
`ci`, `assert`, `can`, `why`, `audit tail|stats|export|verify`,
`action log tail|stats|verify|export`,
`task status`, `task validate`, `task check-done`, `run status`, `run log`,
`session-briefing`, `done`, `proof`, `start`, and `tui` never compile or write
the lockfile. Missing, stale, malformed, schema-drifted, or non-portable current
lockfiles fail closed with one explicit remediation: `reconc refresh .`. The
pre-command gate admits exactly that repair while everything else stays
blocked, otherwise the stale lockfile would seal the session. The exemption
requires a Reconc binary the environment vouches for: inside the repository
only the bootstrap-managed `tools/reconc/` tree qualifies, so an executable an
agent writes into the repository under the product name does not inherit it,
while an installed CLI outside the repository still does.
When `RECONC_AUDIT=1`, enforcement commands may still append decision records;
that opt-in audit write is independent of policy refresh. Explicit `check`,
`ci`, and `done` decisions may also write or clear one
private unresolved-block receipt below `RECONC_HOME`; governed worktree content
remains untouched.

CI-native output is a presentation of the same policy decision, not a second
engine. `reconc check` and `reconc ci` accept `--format sarif` for SARIF 2.1.0
code-scanning consumers and `--format junit` for JUnit report consumers. SARIF
maps observe/warn/block-or-fix to note/warning/error. JUnit maps observe/warn
to successful diagnostic cases, block/fix to failures, and operational
evaluation failures to errors. Findings without an exact source location stay
rule-level; matched paths receive repository-relative URI-safe artifact
locations without invented line numbers.

The shared neutral report model includes bounded rule, mode, message,
remediation, matched-path, candidate-fingerprint, policy-lock, worktree, and
optional Git-range metadata. It excludes absolute host identity and escapes
JSON, XML, URI, terminal-control, and workflow-command content. Outputs are
deterministic, capped at 1,024 findings and 8 MiB, make no network call, and
are atomically published by `--output` before the exact same bytes reach
stdout. Existing text/JSON/terse defaults and exit codes do not change.

```bash
# GitHub Code Scanning; map RECONC_BASE_SHA to github.event.pull_request.base.sha
reconc ci . --base "$RECONC_BASE_SHA" --head "$GITHUB_SHA" --format sarif --output reconc.sarif

# GitLab artifacts:reports:junit input
reconc ci . --base "$CI_MERGE_REQUEST_DIFF_BASE_SHA" --head "$CI_COMMIT_SHA" --format junit --output reconc-junit.xml

# Generic JUnit consumer, including Jenkins or Azure Pipelines
reconc ci . --base origin/main --head HEAD --format junit --output reconc-junit.xml
```

The current v0.9.7 source can export the same completion candidate for external
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
receipts expose a redacted executable summary plus a SHA-256 identity of that
sanitized executable only. `--output` atomically writes the exact stdout
bytes.
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
model, daemon, Docker, Node, Python, or network access. Supported hosts own
their model authentication and inference traffic. Codex and GPT-5.6
contributed to development during OpenAI Build Week but are not runtime
dependencies. Installation, Git operations, and remote CI naturally use the
network when invoked.

### Which agent runtimes are supported?

The registry owns integrations for Claude Code, Codex, GitHub Copilot, Cursor,
OpenCode, Devin CLI, Antigravity CLI, Kilo Code, Oh My Pi, Pi, ZCode, Grok Build,
and Kimi Code CLI, plus git pre-commit as the repository backstop. Host capabilities
differ: some expose synchronous Stop, GitHub Copilot and Kimi Code retain
documented host-fail-open timeout behavior, OpenCode and Kilo expose inferred
`session.idle`, OMP exposes awaited main-session `session_stop`, Pi exposes
inferred asynchronous `agent_settled` continuation, and Grok has a
strict ACP driver for continuation. Run
`reconc hook status . --json` before claiming that a particular installation
is live.

### How do I install and test it?

Use the immutable v0.9.6 POSIX installer for macOS or Linux and the immutable
v0.9.6 PowerShell installer for Windows x64. Put the installed binary on
`PATH`, verify it with `reconc doctor --global`, and initialize the target
repository with `reconc init .`. Contributors building current source can use
`go build -o .build/bin/reconc ./cmd/reconc` followed by
`.build/bin/reconc install-cli`; copied repo-local binaries use the same
one-time `install-cli` call.

### How does repository bootstrap work?

`reconc init .` is the canonical path. It composes deterministic bootstrap
inspection, selection, planning, create-only apply, durable plan and receipt
publication, and verification. Drift gets a hash-addressed review candidate;
the portable receipt makes later sync and ownership-bounded removal explicit.
Use an explicit `existing` profile
for mature repositories that already own policy, instructions, docs, and TASK
state. Advanced `bootstrap inspect|profiles|plan|apply|verify|remove` commands
expose the same engine without duplicating business logic. `--profile advanced` additionally
materializes the immutable public harness pack from the installed binary, binds
its version and digest into the plan and receipt, and requires no toolkit copy.

### How do I update Reconc?

Run `reconc update`. That single command verifies installation ownership,
selects stable by default, requires GitHub build-provenance verification,
applies an available update atomically, and succeeds without mutation when
already current. Equal semantic versions count as current only when the
installed receipt SHA-256 matches the selected release artifact, so corrected
same-version bytes still follow the full verified replacement transaction.
Semantic-version precedence compares numeric prerelease identifiers by digit
length and ordinal text, so valid identifiers are not bounded by machine
integer size; the POSIX installer fixes collation to the C locale and the
Windows installer uses ordinal comparison.
Offline `--from-dir` updates require the asset's Sigstore
bundle and trusted root in addition to the strict release inventory. There is no
separate check/apply step in the current user flow. Use `--channel preview` or
`--version VERSION` only for an intentional non-default selection. A
source-owned installation receives the exact rebuild and path-qualified
`install-cli` remediation instead of being overwritten.

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
Git for Windows supplies it. CI runs focused Windows-native contracts on every
candidate and the complete native suite at the bounded checkpoints described
below; the clean-repository self-host golden path currently runs on Ubuntu and
macOS.
Windows cannot represent POSIX permission bits: Reconc validates protected
current-user-only DACLs for private state and uses the readonly attribute as
the representable atomic-file mode boundary. The Windows candidate job runs a
focused four-minute native filesystem, hook, and runtime preflight immediately
after Go module download, then always builds and smokes the Windows binary and
exercises the native installer. It skips the slow all-package suite and
Node/Bun setup on pull requests and candidate-branch pushes. The complete
native suite still runs with two package test binaries at a time after every
accepted update to the default branch, on an explicit manual dispatch with
`full_windows: true`, and unconditionally against an exact tag in the Release
workflow.

### Is the private production repository public?

No. Reconc is dogfooded in a large private codebase for an agentic enterprise
platform being built for the author's startup. Public claims use only generic
behaviour and sanitized aggregate evidence. This standalone repository does not
claim byte-identical parity or disclose private source, architecture, prompts,
or task details.

## Troubleshooting

Start with read-only diagnostics:

```bash
reconc --version
reconc doctor --global
reconc status .
reconc doctor . --deep
reconc sources . --json
reconc audit verify . --json
reconc hook status . --json
reconc hook evidence-status . --json
reconc task validate . --json
reconc run status . --verbose
```

If policy sources changed, run the exact remediation `reconc refresh .`; read
commands never refresh implicitly. If a block is valid but unclear, run
`reconc next .`. For a saved transactional bootstrap, use `reconc bootstrap
verify --plan PATH --json`. For the current portable ownership state, use
`reconc repo sync verify . --json`; plan a repository upgrade with
`reconc repo sync plan .` before writing a plan file. If a previous sync was
interrupted, run `reconc repo sync recover .` before any other repository
transaction. Recovery finalizes a fully published verified after-image, rolls
back only exact journaled before/after images, and refuses to overwrite an
external edit. Resolve a reviewed blocking action with the exact emitted
`reconc repo sync resolve` command; never delete the receipt or transaction
journal to bypass ownership. Use `reconc task recover .` only for an interrupted
TASK transaction and `reconc run reset .` only for corrupt or foreign run
state. Do not delete lockfiles, receipts, managed blocks, or runtime state as a
generic repair strategy.
If bootstrap reports that the running build is not directly callable, run the
exact path-qualified `install-cli` remediation it prints, apply any emitted PATH
line, open a new terminal, and retry. Do not work around the check with
versioned paths.

For a LangChain gateway launch, initialize the selected private operator state
once with `reconc action key init --reconc-home /private/operator/reconc-home`.
An existing key is never replaced. `identity-key.json` missing means the
operator initialization step was skipped; a repeat-init error means a valid
generation already exists and must be preserved. The pinned legacy LangChain
client can complete approval only through standard MCP form elicitation by
returning an externally signed receipt from the configured authority. A client
without the required elicitation capability, callback, or valid receipt fails
closed and does not dispatch the downstream tool. Policy or lock drift is
repaired with an explicit `reconc refresh .` followed by a reviewed new lock
digest and a restarted operator-pinned gateway, never by weakening the launch
to repository-managed authority silently. `reconc status . --json` and
`reconc doctor . --deep` deliberately report that external client
configuration is not inspected and direct/native bypass routes are unenforced.

## Upgrading

Run the complete ownership-aware update:

```bash
reconc update
```

The command selects stable by default, verifies the release and current
installation, applies an available update atomically, and succeeds without
mutation when already current. Equal version text alone is insufficient: the
receipt artifact SHA-256 must match the selected release asset. Use
`--channel stable|preview` or
`--version VERSION` only when that selection is intentional. Exact-version
downgrades and channel changes require explicit flags.
The current user journey has no separate check/apply step: this one command
performs the verified decision and the safe update transaction.
Direct installations download only the immutable manifest-selected asset,
verify version, checksum, and required provenance, smoke-test a sibling
candidate, and atomically replace only the receipt-owned binary. Source,
unowned, ambiguous, shadowed, read-only, and unsupported installations return
a non-mutating remediation.

After an update, run:

```bash
reconc version --json
reconc status .
reconc doctor . --deep
reconc refresh .
reconc hook status . --json
reconc repo sync verify .
```

`refresh` performs registered lockfile migrations and recompiles current policy;
it never silently rewrites source rules. A global binary update does not update
repository-owned hooks or harness artifacts. Run `reconc repo sync plan .
--output PATH`, review every action and exact digest, then run `reconc repo
sync apply --plan PATH --digest SHA256`. Resolve each reviewed blocking action
with `reconc repo sync resolve --plan PATH --digest SHA256 --path RELATIVE
--strategy keep-current|use-target|use-binary`, then rebuild and review a fresh
plan. If an interrupted transaction journal exists, run `reconc repo sync
recover .`; a `refused` result means a journaled path has an external edit and
requires operator review. Never bypass drift or recovery by deleting the
receipt or journal. Commit the refreshed portable policy lock and
`.reconc/install.lock.json` in governed target repositories.
For a locally built or copied upgrade, run the upgraded binary's `install-cli`
command first so bare `reconc` cannot continue resolving an older build.

## v0.8.8 To v0.9.0 Migration

Preserve the installation owner and update the global binary before any
repository-owned files:

| Existing v0.8.8 state | Migration |
| --- | --- |
| Direct POSIX install | Run the immutable v0.9.0 `install.sh --version 0.9.0`, apply its exact PATH remediation if needed, then run `reconc doctor --global`. |
| Direct Windows x64 install | Run the immutable v0.9.0 `install.ps1 -Version 0.9.0`, apply its exact PATH remediation if needed, then run `reconc doctor --global`. |
| Source build or copied binary | Build or select the v0.9.0 binary and run its path-qualified `install-cli`; never copy it over the current global target manually. |
| Copied standalone toolkit or private bootstrap receipt | Keep every user-owned file. Run `reconc init .` for an explicit profile or build `reconc repo sync plan .`; legacy receipts are bounded migration evidence, not permission to overwrite drift. |

Then verify the two independent layers:

```bash
reconc --version
reconc doctor --global
reconc repo sync plan . --output /tmp/reconc-v0.9-sync.json
reconc repo sync apply --plan /tmp/reconc-v0.9-sync.json --digest <plan-digest>
reconc repo sync verify .
reconc status .
reconc hook status . --json
```

Review the saved sync plan before apply. `user-drift`, `orphaned-legacy`,
`incompatible`, and `manual-review` perform no repository mutation. The v0.9
transaction changes only receipt-owned files and exact managed blocks, rolls
back its own already-published bytes on failure, and preserves policy sources,
agent instructions, docs, TASKs, and unrelated repository content.

## v0.9.0 To v0.9.1 Migration

v0.9.1 supersedes v0.9.0 for direct Windows installations. The v0.9.0
PowerShell installer can fail before publication when a real HTTPS response
exposes `Content-Length` as a numeric value. v0.9.1 accepts both a missing and
a numeric header while retaining the 2 MiB metadata limit, 256 MiB binary
limit, streamed byte cap, checksum, provenance, and atomic publication gates.
The failure does not replace an existing binary.

Preserve the current installation owner:

- Direct Windows installs rerun the immutable v0.9.1 `install.ps1 -Version
  0.9.1`, then run `reconc doctor --global`.
- Direct POSIX and source installs update through their existing exact-version
  path when desired.
- Repository receipts, policy locks, hooks, schemas, and the embedded
  `advanced@1.0.0` harness pack require no patch-specific migration.

## v0.9.1 To v0.9.2 Migration

Update the global CLI through the existing installation owner:

```bash
reconc update
reconc doctor --global
```

Exact native installs may instead rerun the immutable v0.9.2 installer.
Source-owned installs build the v0.9.2 source and run that binary's
`install-cli` transaction. The update changes only the globally owned CLI and
receipt. It never mutates a repository.

Repository-owned hooks, harness files, and generated artifacts remain an
explicit, reviewable transaction:

```bash
reconc repo sync plan . --output /tmp/reconc-sync.json
reconc repo sync apply --plan /tmp/reconc-sync.json --digest <plan-digest>
reconc repo sync verify .
```

Review every action and resolve any drift before apply. Reinstall a specific
agent hook only when that repository should receive its updated generated
adapter. Kimi Code remains an explicit user-global opt-in through
`reconc hook install kimi-code`; `init` and repository sync never select it.

No policy or schema migration was required by v0.9.2. That release continued
accepting and emitting v0.9.1 schema URLs. Current source retains those URLs
only as explicit legacy aliases because several named files were absent or did
not contain the later local bytes; new artifacts never emit those aliases.
TASK transaction journals, repository-sync journals, and ownership receipts
remain format-compatible. The removed `demo` command and retired legacy
command aliases do not return in v0.9.2.

## v0.9.2 To v0.9.3 Migration

Update the global CLI through the existing installation owner:

```bash
reconc update
reconc doctor --global
```

Exact native installs may instead rerun the immutable v0.9.3 installer.
Source-owned installs build the v0.9.3 source and run that binary's
`install-cli` transaction. The update changes only the globally owned CLI and
receipt. It never mutates a repository.

ZCode is now a native runtime integration. Repositories that use ZCode can
install its generated adapter explicitly and verify the result:

```bash
reconc hook install zcode . --json
reconc hook status . --json
```

The adapter reads `.zcode/config.json`, supports the documented seven ZCode
events, and enforces the native synchronous Stop contract where the host
provides it. Existing repository hooks and generated artifacts remain
untouched until an explicit install or repository-sync transaction.

No policy or schema migration was required by v0.9.3. That release continued
accepting legacy v0.9.1 identities; current source classifies them as input-only
aliases rather than verified publication locations. Test measurement output
remains review evidence only; Reconc has no fixed percentage requirement.

## v0.9.3 To v0.9.4 Migration

Update the global CLI through the existing installation owner:

```bash
reconc update
reconc doctor --global
```

Exact native installs may instead rerun the immutable v0.9.4 installer.
Source-owned installs build the v0.9.4 source and run that binary's
`install-cli` transaction. The update changes only the globally owned CLI and
receipt. It never mutates a repository.

The policy lockfile moves to format version `4`, published as the v0.9.4
`schemas/v4/policy-lock.schema.json` identity. Format 1, 2, and 3 lockfiles
migrate automatically on read, so no repository action is required; `reconc
refresh .` rewrites the lock in the current format whenever the policy sources
change. The v0.9.1 schema URLs remained accepted compatibility identities at
that release. Current source never emits them and does not claim that those
locations contain the later schema bytes.

Format 4 carries one new optional field. A `require_script` rule, or a
`require_script` check inside a composite rule, may declare `cache_inputs`: the
literal repository-relative paths the script reads. Each entry may be a file or
a directory.

```yaml
rules:
  - id: schema-drift
    kind: require_script
    when_paths: ['src/**']
    script: 'scripts/check-schema-drift.sh'
    cache_inputs: ['build/schema-report.json']
    mode: block
    message: schema drift gate
```

Stop report reuse binds those paths exactly. Declaring `cache_inputs` is also
an author contract that the script result is a deterministic function of its
script, policy arguments, Reconc execution input, and the declared paths'
content plus supported metadata. A script that reads wall-clock time,
randomness, network state, mutable ambient environment, an undeclared path, or
unsupported filesystem metadata such as access time, ownership, ACLs,
extended attributes, or the link object behind a followed symlink must leave
`cache_inputs` absent. Such a gate is never reused and runs on every applicable
Stop. Globs, template
variables, escaping paths, and duplicate entries are refused at compile time,
because binding them would require a search on the Stop path.

Hook routes were added after verifying the hosts' own configuration surfaces.
Codex accepts `SessionEnd` among its eleven matcher groups, and Claude Code
accepts `Notification`; both are now generated. Claude Code and Codex also gain
a matcher group for the `mcp__<server>__<tool>` namespace, which makes MCP
policy enforceable on both hosts, and Codex's `SessionEnd` timeout is declared
as the three seconds that host accepts instead of a value it clamps.
Repositories that installed hooks before the upgrade keep working, and their
installed artifacts report as stale until they are reinstalled:

```bash
reconc hook status . --json
reconc hook install claude-code . --json
reconc hook install codex . --json
```

## v0.9.4 To v0.9.5 Migration

Update the global CLI through the existing installation owner:

```bash
reconc update
reconc doctor --global
```

Exact native installs may instead rerun the immutable v0.9.5 installer.
Source-owned installs build the v0.9.5 source and run that binary's
`install-cli` transaction. The update changes only the globally owned CLI and
receipt. It never mutates a repository.

No policy or schema migration is required. Policy locks remain format `4`, and
the immutable v0.9.4 `schemas/v4/policy-lock.schema.json` URL remains their
canonical schema identity. Stop report reuse is stricter: policy-declared
inputs are reused only while their exact supported content and metadata
identity remains trustworthy; oversized trees, escaping symlinks, special
files, and unstable inputs evaluate without reuse.

Oh My Pi now persists bounded, source-free `user_python` execution metadata in
hook liveness and exposes it through `reconc hook status`; Python source is
never stored. Repositories using OMP must refresh the owned extension to receive
that route:

```bash
reconc hook install omp . --json
reconc hook status . --json
```

Other repository artifacts remain untouched until an explicit hook install or
repository-sync transaction.

## v0.9.5 To v0.9.6 Migration

Source-owned installations build version `0.9.6`. Exact native installations
use the immutable `reconc-v0.9.6` release. Version text alone is not release
identity: verify the tag, checksums, manifest, and provenance.

Policy locks move to format `6` under the immutable v0.9.6
`schemas/v6/policy-lock.schema.json` identity. Formats 1 through 5 migrate in
memory; `reconc refresh .` intentionally persists the current format. Format 6
contains one canonical `actions` plan and never a parallel runtime `mcp` plan.
Legacy `mcp` authoring remains accepted and lowers deterministically into that
plan. Public schema ownership is per artifact: all 36 contract versions are
registered with exact local bytes, digest, release asset, immutable URL,
enterprise path, and compatibility aliases. Policy config uses v4;
repository-sync plan/report and custom-runtime manifest use v2. Existing
supported legacy inputs remain accepted. New output emits only the current
registered identity, and runtime validation stays offline.

`reconc diff LOCK-A LOCK-B` compares the migrated current envelopes rather than
raw JSON text. Its typed JSON and text reports classify every top-level field as
semantic, provenance, generated, or unsupported; show action-plan and other
envelope changes; report source inventory additions, removals, content changes,
moves, and order changes; and expose rule source-provenance moves separately
from semantic rule changes. Only explicitly set-like rule fields are sorted for
comparison. Ordered lists such as command arguments, source precedence, and
source inventory retain their order, so a reviewer never loses a meaningful
ordering change behind generic digest drift.

Impact corpora now emit `reconc-impact-corpus/v2`. Existing strict v1 corpora
migrate deterministically to repository cases. Format 2 adds offline
`action_pre` and `action_post` scenarios, exact current assertions, completeness
claims, and reviewed candidate-delta gates. Candidate policy files may add
`actions` declarations in memory; this does not alter live policy or enforce a
live tool call.

Action authoring may now include strict cumulative budgets. Their compiled
declarations and evaluator snapshots are format-6 data. Durable reservations
live only under the operator-selected `RECONC_HOME`, never in the repository,
and are opened only when an operator explicitly launches `reconc mcp gateway`.
Existing repositories therefore gain no live interception or implicit state
mutation merely by compiling or refreshing policy. A filesystem root is never
accepted as `RECONC_HOME`, and an existing selected root must already be private
because Reconc never changes its permissions implicitly.

Action authoring may also declare `actions.approvals` to select safe argument
summaries for an external approver. The current source implements canonical
one-call requests, Ed25519 approve or reject receipts, strict operator-owned
authority registries outside the repository, single-use replay protection,
atomic budget coupling, expiry reconciliation, exact MCP `2026-07-28`
input-required plus MCP `2025-11-25` form-elicitation transport mapping, and
payload-free transition evidence. It
does not add a public approver or dashboard. `reconc mcp gateway` accepts a
signed one-time receipt from the upstream MCP client only when an operator-owned
authority registry verifies it. Authority keys and the signing process remain
operator-owned and cannot be supplied by repository policy or agent input.

Action authoring may now declare `actions.detectors`. The compiler binds the
built-in detector-pack ID and digest, exact phase-compatible RFC 6901 fields,
categories, dispositions, schema policy, supported content policy, exact
fingerprint-bound annotation trust, and hard inspection limits into format 6.
The internal Go inspection core strictly decodes bounded MCP tool results,
validates local Draft 2020-12 output schemas without remote references and with
a bounded RE2-compatible pattern subset, scans selected arguments, results,
and progress deterministically, and requires every returned content block to
be fully selected or explicitly type-allowlisted. Unselected structured
content, metadata, unknown content, and untrusted annotations fail closed. The
core emits only payload-free evidence or a bounded withheld result.
`reconc mcp gateway` applies this core to arguments before dispatch and to
progress and results before upstream delivery.

Action authoring may now declare `actions.ledger` with recording mode
`required`, `best_effort`, or `off`, one declaration/exact/keyed tool-identity
mode, and bounded selected argument or result pointers. Format 6 binds this
policy into the canonical plan. The separate private ledger stores only typed,
payload-free lifecycle records in a bounded retained hash chain with two
archives, a detached head, and crash recovery. `reconc action log
tail|stats|verify|export` verifies before reading, never creates missing state,
and reports lifecycle gaps instead of inferring success. Export contains only
verified synthetic minimized Impact Lab cases, explicitly lists every omitted
call and missing raw dimension, never claims complete replay, and refuses to
replace an existing output file. In `ledger: required` mode, the gateway must
record the accepted request and pre-decision before downstream dispatch or fail
closed without invoking the tool.

Budget ledger transitions mirror persisted state changes. A `denied` event
therefore binds the live reservation it closes, the released reserved capacity,
and denied-count consumption only. If reservation itself was refused because a
budget was already exhausted, the blocking `pre_decision` is the complete fact;
the ledger never fabricates a reservation or counter delta.

`reconc action evidence export|verify` reads those private action surfaces
without creating or repairing state. The report schema, control-map schema,
control-map signature, and mapping-authority registry are versioned v1
artifacts. Export requires an explicit canonical UTC `--as-of`, records exact
window and retained-history boundaries, derives every control status from a
closed evidence-fact set, and emits no raw arguments, results, receipts,
credentials, headers, environment values, paths, or personal data. Verification
rejects an `--as-of` before the latest retained record, rebuilds current evidence,
and exits successfully only when every selected
mapping is `covered`. These commands provide technical evidence and mapping,
not organizational assessment, legal determination, or external assurance.

## Uninstall And Remove

Prefer receipt- and registry-owned removal:

```bash
reconc uninstall
reconc bootstrap remove --plan <plan-path-from-init> --json
reconc hook uninstall codex . --json
```

Global uninstall verifies ownership and checksum identity before mutation.
Direct and source installations remove only their receipt-owned binary and
receipt.
`--purge-state` additionally requires a complete known global-state inventory
before mutation and fails if an unknown entry exists. The persistent private
installation lock and its directory remain as coordination state. Repository
state is always retained.

Bootstrap removal verifies the portable receipt and current hashes, removes
only exact owned files and generated artifacts, strips only exact marker-owned
blocks, preserves every user-owned path and outside byte, and refuses drift.
Private receipts cannot expand that authority. Repeat
`hook uninstall` only for separately installed runtime kinds. Reconc
intentionally does not delete user-owned
`.reconc.yml`, `AGENTS.md`, TASK files, or policy source; review those
manually. `reconc prune .` bounds owned runtime residue but is not a substitute
for uninstalling policy or user data.

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
with stable issue IDs and exact remediation. An absent `done_visible` uses the
default of 10; an explicit value must be between 1 and 1000, so zero and
negative values never silently select the default.

`completion.required_sections` and `completion.required_evidence_fields` may
each contain at most 32 unique one-line names of at most 120 characters.
Briefings expose at most five TASK blockers, three policy gates, and six
missing evidence fields; each free-text value is capped at 240 characters and
omitted counts remain explicit.

Once `task_lifecycle` is explicitly present, its overview path is mandatory:
missing, unreadable, unsafe, or invalid TASK state fails closed instead of
degrading to `absent`. `completion.require_committed: true` additionally blocks
terminal TASK completion while the configured overview or detail tree is dirty.
All TASK config, overview, runtime-state, detail, and archive paths use one
identity-aware component guard. It rejects symlink/reparse and irregular
components, non-directory intermediates, and replacement identities; the fast
run-state reader and complete inspector bind the overview, every detail they
read, every component identity, and the absent transaction journal to one
before/after snapshot. Changed bytes, disappearance, inode replacement, a new
symlink, or an appearing transaction fails with
`task/read/concurrent-mutation`.
The terminal gate reuses the single Git status snapshot already built for Stop;
it adds no Git process to routine executable continuations.
Portable workflow audits reserve `docs/tasks/` and `docs/tasks/done/` for
referenced `TASK-NNNN-Name.md` details. An unreferenced conforming detail keeps
the atomic `promote-task-done` remediation; any other Markdown file is reported
as a non-TASK file and must be moved outside the reserved tree. `.gitignore`
cannot clear this filesystem audit.

`reconc done` is the public evidence-complete final gate. A versioned,
self-digested completion report binds the policy lock, Git
HEAD and logical index, worktree contents, dirty paths, active-session evidence,
saved session report, current policy result, current staged command proofs, and
typed TASK completion. Its snapshot owns the exact evaluator input: Git dirty
paths and relativized epochs when Git is available, otherwise session paths and
epochs, plus the staged command proofs loaded at capture time. It also binds the
concrete targets selected by dynamic evidence and freshness templates, their
trusted filesystem identities, native-assurance authority observations, and
the current temporal freshness verdict. Policy evaluation consumes that
captured input rather than rebuilding it. Candidate state and every dynamic
identity are captured before and after evaluation; any change aborts the gate.
A blocking explicit `check` or `ci` decision for the
same candidate remains unresolved until a later explicit non-blocking decision
clears its tamper-evident receipt. Time and retention never clear it. With no
typed TASK lifecycle, the TASK check is a minimal pass; configured lifecycle
state must be terminal and satisfy every required section, evidence field, and
optional committed-control-plane rule. `--require-clean-git` adds a clean-tree
check. Elapsed time is never completion evidence.

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
image with its recorded regular-file type and permission mode, so an external
edit is never overwritten. Recovery resolves and validates every rollback path
before its first mutation and propagates every unsafe-path failure. Before
publication, the transaction revalidates
every source, destination, content hash, and mode both as a complete set and
immediately before its operation. Moves retain the source before-image and use
a no-clobber hard-link transition; recovery safely recognizes the linked
intermediate state. Journals reject unknown fields, trailing JSON values,
non-canonical paths, and inconsistent images. Archived detail bodies are not
reopened by normal status or briefing reads. Runtime paths reject symlink
components, journals are capped at 4 MiB, and rollback restores the original
file bytes and permission mode. Running `task recover` with no journal is a
successful idempotent no-op and reports `recovered: false`.

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

- `status` - one-line policy health summary
- `check` - evaluate runtime evidence against compiled policy
- `next` - show the next remediation
- `done` - task-finish gate
- `proof` - deterministic portable completion proof
- `impact` - offline current-versus-candidate policy replay

Bootstrap and inspection:

- `bootstrap` - inspect, profile, plan, apply, verify, or remove onboarding
- `repo` - plan, resolve, apply, recover, or verify receipt-owned sync
- `init`
- `adopt`
- `extract`
- `doctor`
- `install-cli`
- `update`
- `uninstall`

Compile and evaluate:

- `refresh`
- `sources`
- `ci`
- `policy` - validate, explain, and explicitly adopt a policy fragment
- `exec`
- `assert`
- `can`
- `diff`

Explain and remediate:

- `explain`
- `fix`
- `why`

Packs and wiring:

- `mcp` - run one enforcing tools-only stdio gateway
- `preset`
- `template`
- `hook`

Workflow maintenance:

- `action` - verify, query, summarize, minimize, or map retained action evidence
- `agent-intro`
- `audit`
- `run`
- `task`
- `prune`
- `session-briefing`
- `context`
- `start`
- `tui`

Meta:

- `completion`
- `manpage`
- `version`

For exact flags, run `reconc help <command>` or
`reconc <command> --help`; nested targets such as
`reconc help task recover` are supported.

## Repository Policy

In governed target repositories, repo-local policy lives in `.reconc.yml` and
should be committed. The generated `.reconc/policy.lock.json` is a portable,
committable policy contract and should be reviewed with policy-source changes.
Format 6 is checkout-independent and byte-identical across equivalent clones
and worktrees. Source records contain only portable logical paths, SHA-256
content identities, kinds, and bounded inline locations; raw source bodies and
physical global-policy paths never enter the committable lock. Its
`lock_digest` binds the complete canonical payload except for the digest field
itself. For current format-6 locks, runtime verifies that envelope and reads the
bounded source bundle once to compare its complete identity digest, then
strictly decodes one typed immutable repository-rule and action plan. Format-1
lockfiles are migrated in memory only after their legacy schema and historical
source digest over `source_precedence` plus raw `sources` pass. That digest
verifies source records, not the whole format-1 payload because `lock_digest`
did not exist yet. Formats 2 through 5 require their legacy schema and
whole-payload digest to pass before migration;
their sources are reparsed and must retain exact embedded rule and canonical
action parity.
The lockfile boundary performs a bounded recursive
`encoding/json/jsontext` admission scan before allocation: it rejects duplicate
keys, invalid Unicode, more than 1,048,576 aggregate JSON items, excessive
nesting, non-object roots, and trailing values. A strictly admitted current
format is then decoded directly into one typed envelope. Its rules and actions
remain raw JSON subtrees in the compatibility payload, are decoded once into
typed plans, and rule/check/assurance field presence is collected with a token
walk rather than maps of `json.RawMessage`. Current large arrays are therefore
never boxed into `interface{}` or re-marshaled to recover their bytes. Formats
1 through 5 retain the generic migration table and exact historical digest
semantics. The action-value boundary uses the same strict token API while
preserving decimal normalization, aggregate cardinality, string, numeric,
argument-size, and depth limits plus the public error-kind contract.
Source compilation uses an evaluation-scoped `SourceLoadContext`: discovery,
canonical root identity, config identity, and the per-default-glob fragment
inventory travel together through one load and are revalidated before and
after reads. Default fragments therefore are not globbed twice, while
additional validated includes still expand independently.
The ingest boundary owns the canonical compiled-lockfile path used by
discovery, compilation, publication, runtime reads, bootstrap output, doctor,
CLI help, and generated repository metadata. Successful publication produces
an independent post-publication discovery snapshot: it marks that exact path
present and removes only the discovery-owned missing-lockfile warning, leaving
the pre-publication result and unrelated diagnostics unchanged.
Policy source ordering is explicit and stable for all source kinds, including
the custom-runtime rank. It orders provenance and candidate insertion only;
duplicate rule IDs are never cross-tier overrides. The parser rejects same-tier
and cross-tier duplicates with the ID plus both source paths, and the lockfile
continues to serialize the unchanged eight rule-bearing precedence entries for
compatibility.
Glob expansion is segment-based and bounded before source bodies are retained:
patterns are capped at 256 and 1 KiB each, directory enumeration and matches
are capped, only regular files are candidates, and `**` is not recursive
special syntax. Duplicate paths are removed by normalized repository-relative
identity before reading.
The compiler configuration is decoded once into the authoritative YAML
mapping; `include` and `extends` extraction then share that document while
retaining their existing strict field/type errors.
Every retained rule-bearing source is likewise syntactically decoded once into
one bounded YAML document. Rule, scope, default-mode, and MCP validation consume
its typed mapping while canonical action validation consumes the same retained
node tags and source positions. Duplicate keys, aliases, nesting, scalar-byte
limits, and trailing documents therefore have one authoritative parser boundary
without weakening strict action scalar validation.
At the compile render boundary, each source is converted once into an
immutable provenance record containing its logical identity and content hash.
The aggregate source digest and emitted lock payload consume those same
records, preventing divergent duplicate hashing or map construction.
Canonical JSON normalization returns the validated `UseNumber` value and its
canonical bytes together. Action parity checks consume those bytes directly,
preserving number fidelity, custom marshaling, null/empty distinctions, and
trailing-value rejection without a second marshal/decode cycle.
Canonical action values pre-size one output slice and encode recursively into
it. Strings use `jsontext.AppendQuote`; the small HTML and JavaScript separator
set retains the historical `encoding/json` escaping path, preserving
identity-bearing bytes. Differential tests and fuzzing compare accepted values,
canonical decimals, duplicate-name behavior, Unicode rejection, nesting, and
exact string bytes against the previous decoder and encoder contracts.
Inline fenced-policy blocks are extracted with one authoritative bounded line
scan shared by compilation and deep-doctor reference inspection. Opening and
closing fences must be line-anchored, may carry horizontal trailing whitespace,
and accept LF or CRLF. A per-source block cap is enforced before another block
body is retained; unterminated fences remain prose, and both consumers receive
the same trimmed content, order, path, line number, and block identity.
Repository sources are read through a bounded opened-file snapshot that
returns the file identity used for the bytes. Loader checks compare that
identity, the canonical source path, and the canonical repository root after
the read, so same-path replacement, deletion/recreation, size drift, and
parent/root swaps fail closed.
Publication uses identity-bound atomic replacement and skips the write entirely
when the canonical bytes are unchanged, so readers never see partial JSON and
repeated compiles do not
create needless filesystem churn. Refresh acquires the repository compile lock
before loading the authoritative source bundle, rejects repository-root drift,
and binds the repository, `.reconc` directory, and compile-lock file to opened
filesystem identities. Concurrent refreshes therefore cannot publish an older
pre-lock snapshot after a newer source state. This standalone product
repository does not carry either file and must exercise policy compilation only
inside isolated test repositories. Its ignore patterns remain as a defensive
boundary against accidental local state and for nested bootstrap fixtures.

Policy authoring is strict. Unknown keys at the document, scope, rule,
evidence, composite-check, and TASK-lifecycle levels fail compilation instead
of being ignored. This validation applies only to structured YAML fields;
free-form rule messages and agent prompts remain unrestricted text. Editors and
automation can use `schemas/v4/policy-config.schema.json`; emitted lock, policy
report, completion report, fix-plan, and proof-bundle artifacts keep their separate public
schemas.

The shipped v2 and current v4 policy-config schema files are embedded into the
Go binary for offline authoring validation. Embedded bytes are digest-checked
against the canonical schema registry in tests; schema validation is an early
diagnostic and never substitutes for the parser, compiler, conflict, preset,
template, or runtime checks. ECMAScript regexp matching uses one shared adapter
with a 100 ms match timeout and treats engine failure as a validation failure.

Rule fields are also kind-specific. After template expansion, the compiler
rejects any known field that the selected kind cannot evaluate, including empty
values, and names the rule ID, kind, field, and source path. The canonical
top-level matrix is: `deny_write` = `paths`, `when_paths`; `require_read` =
`paths`, `before_paths`; `require_command` and `require_command_success` =
`when_paths`, `commands`, `command_match`; `forbid_command` = `when_paths`,
`commands`, `command_match`; `couple_change` = `paths`, `when_paths`;
`require_claim` = `when_paths`, `claims`; `require_fresh_file` = `when_paths`,
`required_files`; `require_evidence` = `when_paths`, `evidence`; `all_of`,
`any_of`, and `not` = `when_paths`, `checks`; `require_script` = `when_paths`,
`script`, `args`, `timeout_sec`, `kill_timeout_sec`, `cache_inputs`; and
`require_assurance` = `when_paths`, `assurance`. Every kind additionally
accepts `id`, `kind`, `mode`, `message`, and the deprecation metadata. Composite
checks use the corresponding inline matrix, while generated provenance and
scope fields are lockfile-only. The current v6 lock schema overlays these
constraints on its legacy rule envelope so an edited lockfile cannot restore
ignored fields at runtime.

Template-bearing paths use the grammar owned by `internal/templates`: a token
is exactly `{name}` with ASCII identifier characters, while balanced glob
alternatives such as `{js,ts}` remain valid. Unescaped malformed or unmatched
braces fail at compile time; literal braces must be escaped as `\{` and `\}`.
The same scanner drives masking, capture extraction, substitution, runtime
matching, and compiler diagnostics, so parser and runtime cannot drift on
hyphens, Unicode, repeated variables, or missing bindings.

Policy-controlled file paths are repository-relative contracts. Compilation
rejects absolute, volume-qualified, empty, and parent-traversing paths in
`required_files[].path`, `evidence[].file`, and composite `path`/`file`
checks, including paths after template placeholders are masked for validation.
Before typed rules are retained, the parser applies one bounded YAML contract:
at most 4,096 rules, 256 checks or items in any rule list, 1,024-byte pattern
strings, 16-KiB command strings, 64-KiB message strings, 32 nesting levels,
131,072 YAML nodes, 262,144 alias-expanded nodes, 1,024 aliases, and 4 MiB of
decoded scalar bytes. Duplicate mapping keys, trailing documents, recursive
aliases, and any limit overflow fail closed with source path/block, rule, field,
actual value, and maximum where a rule is identifiable. Required-file,
evidence, assurance, scope, and composite sub-check collections use the same
item and text ceilings, so a source cannot bypass bounds by moving data into a
nested variant. Empty and comment-only rule sources remain valid empty
documents; explicit YAML `null` is rejected because it is not a policy mapping.
User preset manifests enter this same bounded YAML admission path. Template
resolution is cached once per compile by normalized name and exact resolved
source content, then re-resolved before publication so replacement during the
compile fails closed.
Runtime resolves every such path against the filesystem identity of the
repository root and rejects symlink, reparse-point, or missing-tail resolution
that escapes it. One evaluation owns one resolved root identity and one bounded
prospective-path resolver shared by read/write normalization, write epochs,
top-level evidence checks, composite checks, kind-filtered checks, assertions,
and pre-command checks. The root directory entry and reused ancestors are
revalidated during evaluation, while evidence snapshots still revalidate the
resolved file identity and metadata before reusing bytes. Policy sources are
limited to 8 MiB each, 4,096 sources, and
64 MiB in aggregate; compiled lockfiles and execution-input JSON files are
limited to 16 MiB. Execution-input JSON additionally admits at most 64 nesting
levels and 262,144 aggregate tokens/items before generic decoding. Bulk
evidence and ordered events are appended into one capacity-planned accumulator,
preserving bulk-first order and causal write epochs without copying all prior
events for every merge. Evidence and TASK control files are limited to 4 MiB.
An oversized or boundary-escaping input fails closed before evaluation.

The managed target-repository block uses these exact rules. It ignores mutable
runtime state while explicitly re-including `.reconc/install.lock.json` and
`.reconc/policy.lock.json`:

- `/tools/reconc/dist/`
- `.reconc/*`
- `!.reconc/`
- `!.reconc/install.lock.json`
- `!.reconc/policy.lock.json`
- `.reconc/audit.jsonl*`
- `.reconc/cache/`
- `.reconc/locks/`
- `.reconc/reports/`
- `.reconc/run/`
- `.reconc/sessions/`
- `.reconc/task-transaction.json`
- `.reconc/repository-sync-transaction.json`
- `.reconc/bootstrap-*.json`
- `*.reconc-candidate-*`
- `*.reconc-remove-candidate-*`

The standalone source repository additionally ignores defensive nested-fixture
equivalents; the complete source-repository list is in Git Ignore Policy.

Runtime retention is product-owned rather than harness-owned. `SessionStart`
and `SessionEnd` run a cross-process-safe due check with a six-hour interval;
Stop never prunes. `reconc prune [repo] [--dry-run] [--json]` runs the same
core explicitly. Unchanged session files, active-session pointers, reports,
command proofs, and run state are byte-compared and never republished. No-op
session mutations also skip normalization and atomic publication after identity
validation; missing or non-private state and pointer modes are still repaired.
Each registry-dispatched hook request resolves the repository filesystem
identity once into an opaque validated root handle. Payload normalization,
session handling, MCP extraction and enforcement, Stop, compaction, result
adaptation, and liveness reuse that handle; runtime attribution is explicit
request data rather than process-global environment mutation. Existing session
files still validate their stored canonical root, while passive and
workspace-only routes validate any existing state without creating a session,
refreshing the active pointer, or serializing an unchanged state.
Pre-tool and permission routes reuse a bounded external decision only when the
stable tool-call ID, canonical normalized tool input, complete policy-lock
bytes, current policy-source digest, session-state bytes, and project
evidence-taint bytes match exactly.
The identity is sampled again after reading the cache; missing IDs, unreadable
identity inputs, policy or evidence mutation, oversized diagnostics, and
malformed cache entries force a fresh fail-closed evaluation. Session cleanup
removes its decision cache.
Disabled and unchanged hook events do not create run state. Run decisions record every
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
Session-file reads and writes serialize on the same per-session lock. The
repository-wide active-session pointer serializes reads, writes, and cleanup on
its own lock, acquired only after any session lock, so native Windows sharing
rules cannot turn concurrent hooks into lost evidence or failed publication.

Default persistent budgets are 32 session files / 8 MiB / 14 days, 32 reports
/ 8 MiB / 14 days, 128 locks / 1 MiB / 24 hours, 64 staged command proofs /
256 KiB / 24 hours, 16 MiB total external state, and 32 MiB / 14 days for
generated audit binaries. The product-wide project
state root is independently bounded to 256 recognized project roots, 128 MiB,
and 30 days. Explicit prune enforces that global bound immediately; lifecycle
passes protect the current project, live sessions, and roots touched within the
24-hour concurrency grace. A recognized root containing a durable `action/`
state boundary is also protected because generic retention cannot safely return
consumed budget capacity or break action-ledger archive and transaction truth;
action-specific bounded compaction owns that state. The ledger live file,
archives, detached head, and active transaction remain protected together.
Unknown directories are never treated as product-owned. Audit and run-decision
JSONL each use a 2 MiB live file plus two
archives, with per-directory in-process serialization followed by file-locked
append and pre-append rotation. Audit entries
additionally carry one contiguous sequence and SHA-256 previous/current digest
chain, with the latest identity stored in `.reconc/audit.head.json`. Every
audit reader verifies all retained archives, the live file, and the detached
head before returning data. A normal append validates the detached head and a
bounded final live record, then advances the chain head incrementally. Rotation,
recovery, and explicit verification replay the complete retained chain before
accepting or returning evidence. Rotation and chained audit appends publish a
private durable journal with `prepared`, `published`, `committing`, and
`resolved` states plus digest-bound archive backups. Recovery rolls a prepared
append, or a version-2 publication whose commit callback provably never began,
back to the complete pre-rotation snapshot. Once callback execution may have
started, only the owning audit or ledger path may recover it by idempotently
rebuilding the detached head. A successful callback is marked `resolved`
before cleanup, so cleanup-only recovery never repeats it. Legacy version-1
`published` journals remain callback-owned because their state cannot prove
whether head publication started. Resolved recovery removes only transaction
artifacts.
Malformed journals or corrupt backups fail closed and remain available for
diagnosis. Generic retention never rewrites chained audit evidence and fails
on an invalid chain or mismatched ring policy. Repo runtime is capped at 48 MiB. Known
`reconc-proof-neg-*`, `reconc-proof-neg-copy-*`, and
`reconc-proof-gocache-*` temp trees are removed after a two-hour inactive
grace, retaining recent work while removing hard-kill residue before a full
working day passes. Active session/report/lock files, live build-lock targets,
run state/locks, and recent temp trees are never deleted to force a budget.
Global temp and project-root scanning use independent six-hour markers, so
multiple repos do not re-walk either tree on every session start.
Durable publication and CLI output paths propagate write, sync, close, and
unlock failures instead of reporting partial output as success.
The legacy `reconc-prune` compatibility utility applies the documented defaults
to a zero-valued policy, bounds every override, and refuses linked state
directories, linked audit logs, special files, or identities that move before
deletion or replacement. Product-core `reconc prune` remains the normal owner.

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
is not reported as a contradiction. This analysis is deliberately conservative:
it compares exact top-level rule targets and does not recursively prove
composite sub-checks conflict-free. Composite rules are still parsed and
evaluated normally; authors must review their cross-rule interactions.
Conflict-relevant lists are normalized and keyed once, so clean or mostly
unique rule sets avoid pairwise sorting. Pair output remains deterministic and
is capped at 65,536 entries; a final `analysis_truncated` record makes any
pathological overflow explicit instead of allowing unbounded materialization.

Single-identifier brace groups such as `{task_id}` are template captures only
for capture-aware rule kinds. In other glob fields they retain doublestar's
literal brace behavior. Compilation emits an explicit warning naming the rule,
field, and literal interpretation; the warning is advisory rather than a
compile error for compatibility. Capture-aware matching preserves the same
validated doublestar language before and after substitution: `/**/` matches
zero or more directories, leading and terminal globstars retain their segment
semantics, and character classes, alternatives, and escapes remain exact.
Captured values are reinserted as escaped literals and the bound pattern is
validated by the canonical matcher before acceptance.

Command matching is exact (normalized whole strings) by default.
Normalization is wrapper- and anchor-tolerant without weakening semantics: a
complete stack of transparent `rtk ` proxy prefixes is stripped at each
unquoted command position in one input-bounded pass, an absolute repo path
inside `cd` becomes repo-relative, and a leading `cd <repo-root> &&` (or `;`)
anchor collapses away entirely because it is a no-op inside the repo (`||` and
pipe joins are never collapsed). Wrapper normalization is a fixed point and
does not rewrite quoted or escaped data, argument values, backticks, or
`$(...)` command substitutions. Session write epochs recorded under
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

Glob patterns are configuration text and trim surrounding whitespace before
matching. Repository path evidence is normalized to slash separators but never
trimmed: leading and trailing spaces are legal filename bytes and remain part
of the match identity.

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
unresolved dynamic executable names and exhausted nesting fail closed. The
built-in destructive Git guard uses the same model for `git clean` and `git
reset --hard`. It resolves literal inline, local, global, recursive, and
same-command `git config alias.*` definitions before admission. Alias values
and invocation arguments are re-analyzed as executable shell input; dynamic
alias definitions, excessive alias recursion, inspection failures, and unknown
Git subcommands fail closed instead of bypassing the destructive-command guard.

A stale compiled lockfile blocks gated work, because policy cannot be enforced
from a lockfile that no longer describes its sources. The block does not seal
the session: the PreToolUse route admits a lockfile-repair invocation
(`reconc refresh`) even while the lockfile is stale, and the
block message names that escape plus the alternative of reverting the policy
source. The exemption is bounded. It applies only when the failure is the
lockfile contract itself rather than a policy violation, every executable
position in the command must be a repair invocation so a compound command
cannot smuggle work through, analysis must be complete, and the executable must
be `reconc` or a versioned release artifact such as `reconc-0.9.0-darwin-arm64`.
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
including `.devin/`, `.grok/`, `.kilo/`, legacy `.kilocode/`, `.omp/`, `.pi/`, `.zcode/`, and the
other registered platform directories, so plugin dependencies are not mistaken
for product dependency leakage.

`require_assurance` is the native, no-subprocess rule kind used by assurance
packs. The parent `when_paths` controls when the gate set runs. Every gate has
an `id`, `type`, and optional `applicable_if`. Fields that do not belong to the
selected gate type are rejected instead of being silently ignored. Reconc
validates the complete `applicable_if` pattern set before checking whether any
one pattern matches, so an earlier match cannot hide a malformed later pattern.

| Gate type | Contract | Authority surface |
|---|---|---|
| `repository_layout` | Allowed, required, forbidden, hidden, and reserved root ownership | Full repository root |
| `generated_reference` | Configured generator check has current successful command evidence | Current session |
| `language_boundary` | Changed files use configured extensions inside configured zones | Matching changed files |
| `dependency_pins` | Changed package JSON dependency manifests use exact semantic versions or explicit protocol prefixes | Matching changed manifests |
| `package_scripts` | Every configured script that is actually declared and non-empty has current successful manager-scoped evidence; a configured manager must be the sole detected manager, while absent scripts stay optional | Matching package manifests, including inherited workspace manager evidence |
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

`generated_reference` and `live_verification` use `command_policy: all` by
default or `command_policy: any`; compiled locks must contain one of those two
values and malformed values fail during runtime-plan loading.

Substantive proof files use `format_version: "1"`. Each proof record requires a
unique ID, subject, current successful command, `outcome: "pass"`, aggregation
(`last`, `mean`, `min`, `max`, `median`, or `p95`), comparator (`lt`, `lte`,
`eq`, `gte`, or `gt`), numeric threshold and actual, measured samples, an
RFC3339 verification time, and a repository-relative evidence path plus its
SHA-256. Reconc recomputes the aggregate from the samples, compares it to both
the declared actual and threshold, reruns no command itself, and verifies the
evidence bytes. Omitting `max_age_hours` applies the 24-hour authoring default;
an explicit `max_age_hours: 0` disables only the staleness limit. Invalid
timestamps and timestamps more than five minutes in the future always fail.

Native assurance is intentionally bounded: 20,000 changed paths, 4,096 unique
files, 4 MiB per file, 32 MiB total reads, 50,000 applicability or reserved-dir
walk entries, and 50 returned findings plus one explicit omitted-count marker.
An unreadable or over-budget authority surface is an error and fails closed.
Matching gates reuse one canonical path resolution and one bounded in-memory
file snapshot per evaluation, so overlapping source gates do not reread the
same bytes from the SSD. The assurance reader obtains bytes and opened regular
file metadata from one identity-checked `boundedio` snapshot; file and byte
budgets charge that opened snapshot once rather than trusting a pre-open path
stat. The per-evaluation fact graph also reuses normalized
path classes, validated glob decisions, line indexes, package JSON objects,
and compatible Go syntax and canonical-format facts. Package scripts and
dependency pins share one package JSON parser and accept either ordinary UTF-8
JSON or exactly one leading UTF-8 BOM; a second or embedded BOM remains invalid.
Package-script gates memoize each inspected directory's identity, normalized
manager signals,
and parent ancestry within the evaluation; sibling manifests reuse shared
lockfile observations while identity changes invalidate only the affected
chain. Nearest-manager precedence, mixed-manager ambiguity, and partial errors
remain manifest-specific and deterministic. For at least 32 matching Go files,
CPU-only parsing and formatting may use at most four workers
after bodies and byte budgets are claimed deterministically. Findings and
operational errors remain ordered by gate declaration and sorted path, and no
assurance worker starts a process or network request.
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

## Go-Only Action Plane

RECONC-0008 remains Draft. The `v0.9.7` source candidate implements strict
`actions` authoring, canonical format-6 compilation, deterministic lowering of
legacy `mcp` declarations, immutable typed matcher programs, a derived MCP
compatibility view, `reconc why action`, and the transport-neutral deterministic
action evaluator. It also implements canonical authority-bound one-time
approval requests and receipts plus private atomic approval consumption, and a
separate privacy-bounded retained Action Ledger. `reconc mcp gateway` owns one
operator-selected downstream stdio MCP process and applies those controls to
every tool call routed through it.
`reconc impact` invokes that production evaluator for strict offline action
scenarios, exact current and approval-state assertions, candidate deltas,
completeness, privacy, exact inspection evidence, detector deltas, and reviewed
newly-allowed or newly-blocked gates. The internal Go inspection core performs
strict MCP result decoding, local output-schema validation, deterministic
selected-field scanning, fingerprint-bound annotation trust, and bounded result
withholding. `reconc action log tail|stats|verify|export` reads the verified
Action Ledger without creating missing state. Offline simulation and retained
fixtures remain non-enforcement evidence; only explicitly routed gateway calls
cross the live tool-call interception boundary.

The same v0.9.7 implementation provides trusted operator and host context
bindings, domain-separated HMAC identities, explicit key leases and rotation
blocking, compiled cumulative budgets, evaluator budget snapshots, and a
private bounded multi-process action-state store. Budget reservations are
created before dispatch and can be released before dispatch, committed after
dispatch, settled terminally, or retained as indeterminate after uncertain
outcomes. The store rejects stale identities, malformed or oversized state,
clock rollback, partial publication, symlinks, special files, permission drift,
counter overflow, duplicate calls, and capacity oversubscription. This is an
enforcement primitive used by the gateway; it does not make direct MCP or
framework calls enforced.

The action-state owner issues an approval request only after the current
evaluator still returns `require_approval` and an exact applicable budget
reservation is live. The request binds one call, policy and lock, executable,
server and tool contract, trusted principal and context, credential labels,
selected keyed argument identities, taint, repository effect, rule trace,
reservation, issuance, expiry, and nonce. A canonical Ed25519 receipt may
approve or reject that exact request and binds its own canonical signing time
inside the request validity interval. Verification revalidates current trusted
bindings under the state lock, accepts only a key allowed and active at that
signed time by an operator-owned registry, and consumes the receipt at most
once in the same durable transaction that commits its approval budget charge. Malformed,
expired, replayed, cancelled, rejected, unavailable, stale, or unpersistable
transitions fail closed.

Authority is not repository policy. The registry must be a bounded private
regular file outside the canonical repository; the private signing key and
confirmation UI must be outside the agent's authority. `actions.approvals`
selects bounded safe summaries and keyed identities for informed disclosure,
not who may approve. The state consumer accepts only the opaque result of that
trusted registry loader, never registry bytes or keys from a call or repository
input. MCP `2026-07-28` input-required can transport the request,
sealed retry state, and signed receipt, but an unsigned client response has no
authority. MCP `2025-11-25` clients with standard form elicitation can transport
the same signed receipt in one bounded `elicitation/create` exchange. Clients
without the required capability or valid response receive a bounded
approval-required failure.
Startup and pre-work reconciliation atomically expire crashed pending waits so
their pre-dispatch reservations do not remain stranded. Transition evidence
contains only safe labels, timestamps, counters, and bound identities, never
raw selected values, receipts, credentials, or private keys.

The action ledger uses
`$RECONC_HOME/projects/<repository-key>/action/ledger.jsonl`, two bounded
archives, `ledger.head.json`, `ledger.checkpoint.json`, and private lock and transaction files. Its nine
typed events cover request acceptance, pre-decision, approval and budget
transitions, dispatch, downstream outcome, result inspection, final delivery,
and terminal failure. Domain types and strict validation exclude raw arguments,
results, headers, credentials, environment values, stderr, prompts, and
arbitrary metadata. Selected values are represented only by domain-separated
keyed identities. Selection is phase-exact: `pre_call` may bind only argument
fields, `post_result` only result fields, and progress or observation events no
selected fields. Each identity also binds its declaration index and repository
identity, preventing cross-policy, cross-declaration, and cross-repository
correlation. Unavailable identity makes the evidence incomplete with no plain
digest fallback. The chain is tamper-evident within retained evidence, not
immutable or deletion proof against the filesystem owner.
Every live file, archive, lock, journal, and recovery backup is bound to the
same private-filesystem contract as action state: existing ownership, mode, or
ACL drift fails without repair, while each newly created or atomically replaced
file is secured and revalidated. On Windows this requires a protected,
current-user-only DACL for every durable and recovery path. A missing lock is
secured and verified as a private candidate before its final path becomes
visible; concurrent creators converge on that one published lock before any
ledger operation proceeds.

The first append after startup, recovery, checkpoint loss or corruption,
identity-key change, or an external writer fully verifies the retained chain.
The same store may then advance an authenticated checkpoint while the exact
live/archive/head/checkpoint file set and each operating-system change
generation remain unchanged. The checkpoint binds repository and key
identities, detached head and tail, active-call records, and a rolling digest
and count of completed call IDs. It therefore scales with active calls rather
than all completed history. Unix device/inode/change-time and Windows volume,
file ID, change-time, write-time, and attributes prevent restored modification
times from making changed bytes look unchanged. Head publication precedes
checkpoint publication inside the existing recoverable JSONL transaction;
recovery fully verifies retained bytes before rebuilding either summary.

Action-state status and evidence views report the byte length of the already
validated persisted state buffer. They do not marshal the complete state a
second time merely to measure it. Terminal-call admission uses the canonical
sorted state index; state schema and rendering remain unchanged.

Approval status and reason are exact, receipt provenance is all-or-none, and a
terminal budget stop cannot be bypassed by a later approval or dispatch. Unknown
dispatch or delivery state is recorded as incomplete evidence, never inferred.
Read commands create no missing ledger state; an existing durable transaction is
resolved atomically before its verified snapshot is returned.

Rotation refuses to prune a retained beginning for any active call. Tail and
stats therefore expose exact retained-history boundaries rather than silently
closing a lifecycle. Verification distinguishes whether events and calls were
evaluated for completeness from whether they were complete. Stats group only
explicit keyed run and session identities and never turn inactivity, a timeout,
or MCP connection closure into a fabricated terminal event. Export accepts only
declaration IDs or explicitly safe exact tool names; keyed or unsafe names and
selected values remain explicit omissions.

The design keeps all Reconc-owned product and adapter code in Go. The gateway
is one local, tool-only stdio MCP process around one operator-selected
downstream stdio MCP server. It invokes the transport-neutral evaluator and
inspection core before downstream dispatch and before upstream result or
progress delivery. Up to four calls prepare immutable policy and inspection
state concurrently; action-state and ledger stores independently serialize
their durable transitions and retry bounded optimistic reservation conflicts.
Progress uses a 16-event per-call work queue inside the existing 128-event and
1 MiB budgets. Notifications remain source-ordered, a slow upstream sink cannot
block the transport reader, and final result processing waits for admitted
progress to drain or cancel. LangChain launches the Go binary through LangChain's own MCP
adapter; Reconc ships no Python or TypeScript LangChain adapter.

### LangChain MCP interoperability

The exact supported LangChain configuration launches the built Reconc binary
as the stdio server and places the real downstream executable only after
`--`. Initialize the private identity generation once before launch:

```bash
reconc action key init --reconc-home /private/operator/reconc-home
```

```python
from datetime import timedelta

from langchain_mcp_adapters.client import MultiServerMCPClient

client = MultiServerMCPClient({
    "reconc": {
        "transport": "stdio",
        "command": "/absolute/path/to/reconc",
        "args": [
            "mcp", "gateway", "/absolute/path/to/repository",
            "--server", "downstream",
            "--expect-lock-digest", "<64-lowercase-hex-lock-digest>",
            "--principal", "langchain-operator",
            "--role", "automation",
            "--environment", "production",
            "--credential", "database-writer",
            "--run", "run-2026-08-12",
            "--session", "session-001",
            "--approval-authorities", "/private/operator/approval-authorities.json",
            "--approval-policy", "default",
            "--timeout", "60s",
            "--reconc-home", "/private/operator/reconc-home",
            "--",
            "/absolute/path/to/downstream-mcp-server",
            "--downstream-flag",
        ],
        "session_kwargs": {"read_timeout_seconds": timedelta(seconds=75)},
    }
})

tools = await client.get_tools(server_name="reconc")
```

Exactly one policy-authority mode is required. The example is
operator-pinned: replace the placeholder with the reviewed current
`.reconc/policy.lock.json` `lock_digest`. The explicit lower-provenance
alternative is to replace both `--expect-lock-digest` and its value with
`--allow-repository-managed-policy`. Never supply both or neither. Principal,
role, environment, credential labels, run ID, session ID, approval registry,
approval policy, state root, repository, and downstream argv are operator
launch inputs. A credential value is never passed through `--credential`; the
flag carries a safe label only. The approval registry and independent signer
remain outside repository and agent authority.

The supported and continuously tested matrix is exact:

| Component | Proven version or protocol | Proof boundary |
| --- | --- | --- |
| Reconc source binary | `0.9.7` | Built from current source and version-smoked before the test |
| MCP Go SDK | `v1.7.0` | Pinned product dependency |
| Current MCP protocol | `2026-07-28` | Pure-Go raw protocol suite |
| Legacy MCP protocol | `2025-11-25` | Pure-Go raw suite and external LangChain consumer |
| LangChain MCP adapter | `0.3.2` | Official external consumer package |
| LangChain Core | `1.5.4` | Direct tool invocation, no model |
| MCP Python SDK | `1.29.0` | Legacy protocol client used by the adapter |
| Python CI runtime | `3.13.14` | Disposable integration job only |
| Go downstream fixture | format `1` | Test-only Reconc-owned server, not a product adapter |

The external package set is installed from the hash-pinned
`scripts/tests/langchain-requirements.lock`; the test then denies socket
connections. Package download belongs to CI setup, while the proof itself uses
only local stdio. LangChain owns the adapter, Python runtime, MCP session
lifecycle, and package updates. None is linked into the Reconc binary, included
in release assets, or required by product features. Reconc depends on no
LangChain middleware, callbacks, agent state, model provider, or hosted model.
The proof calls converted tools directly and sends no repository data to a
third party.

Five benchmark repetitions on Apple M1 with Go `1.26.5` measured medians of
44.5 microseconds for representative compilation, 72.4 microseconds for
representative serial evaluation, 1.49 milliseconds for the maximum legal
plan, 18.2 microseconds for an exact cache hit, 382 microseconds for approval
verification, 34.5 microseconds for representative text inspection, 9.75
microseconds for representative structured inspection, 39.7 milliseconds for
one durable ledger append, 50.0 milliseconds for budget reserve-and-release,
2.81 milliseconds for discovery with shared schemas, and 7.94 milliseconds for
one end-to-end gateway call. These local observations include the benchmark
fixtures' durability and process costs where applicable; they are review
evidence, not latency guarantees.

Five focused repetitions on the same Apple M1 with Go `1.27.0` measured the
maximum-legal 8 MiB canonical action encoder before and after this change. The
median moved from 16.006 ms, 23,191,586 B/op, and 18 allocs/op to 10.274 ms,
8,388,611 B/op, and 1 alloc/op. The complete structured-action benchmark moved
from 72.828 ms, 22,852,399 B/op, and 118 allocs/op to 41.067 ms, 8,396,832 B/op,
and 100 allocs/op. Allocation reductions are the stable result; latency is a
local observation subject to machine load, not a product-wide guarantee.

The Go 1.27 hotpath pass also bounds trace storage during evaluation, shares
one immutable compiled inspection pack per gateway, traverses immutable action
values without cloning child slices, reuses one freshness hashing buffer per
source batch, right-sizes evaluation-local memos, and opens one rooted source
reader per complete policy load. Five focused Apple M1 repetitions reduced a
maximum legal action plan from 3,184,776 to about 326,700 B/op, a clean maximum
MCP content-array scan from 4,846,336 B/op and 32,777 allocs/op to about
178,300 B/op and 88 allocs/op, a large freshness set from about 5.39 MiB/op to
about 758 KiB/op, and contextual source loading from 407,928 B/op and 4,015
allocs/op to 103,640 B/op and 1,027 allocs/op. The source-load median moved from
8.410 ms to 3.338 ms in the recorded run; filesystem latency remains
environment-sensitive, so the allocation reductions are the portable claim.

Action inspection now consumes read-only compiled detector-policy views rather
than deep-cloning every matching policy, field list, pointer-token list, and
allowlist for each phase. Present and null selected values compute only their
final body-bound keyed identity; missing pointer states retain their explicit
state-bound identity. The common ASCII secret-candidate path uses a stack bitset
and remains Unicode-correct through a bounded fallback set. Evidence selector
admission binary-searches one canonical fact registry instead of rebuilding a
map for every control. Public fact lists remain isolated copies.

Five Apple M1 samples at 100 iterations measured representative structured
inspection at 4,944 B/op and 69 allocations, down from the checked 6,384 B/op
and 90 allocations. ASCII secret diversity and maximum fact-selector admission
both measured zero allocations; their local medians were about 82.5 ns/op and
401 ns/op. Report identity encoding and final canonical report validation stay
independent: the identity hashes compact JSON with an empty identity field,
while publication validates that binding again and emits bounded indented JSON.
Those byte contracts are not identical, so merging them would weaken the final
validation boundary rather than remove safe duplicate work.

`MultiServerMCPClient.get_tools()` uses fresh sessions for discovery and calls;
an explicit `client.session()` plus `load_mcp_tools()` owns one stateful
session. Reconc binds principal, credential labels, run, session, budgets,
approval replay, policy state, and ledger correlation to operator and private
state identities, not to a Python `ClientSession`. Recreating the LangChain
client therefore cannot return consumed capacity or replay a receipt.

At the LangChain boundary, policy block, approval-required, budget exhaustion,
and result withholding are error `ToolMessage` values with bounded safe text
and a stable Reconc reason code. An authoritative downstream tool error remains
a downstream tool error. Transport, session, and conversion failures raise
instead of being converted into a policy result. Structured content is exposed
as the adapter artifact; progress is forwarded through the adapter callback.
The current adapter negotiates only `2025-11-25`; its standard form-elicitation
callback returns an externally signed receipt, which Reconc verifies and
consumes before dispatch. Missing capability, callback, receipt, or authority
still fails closed. Current `2026-07-28` input-required approval is
independently covered by the pure-Go suite.

Coverage remains explicit. Only tools configured to use the Reconc gateway are
enforced. Native LangChain tools, another MCP entry that
points directly to the downstream server, and every other route that does not
launch `reconc mcp gateway` are unenforced. Reconc cannot soundly parse or
certify arbitrary Python configuration. This configuration is intentionally
**unenforced** because it launches the downstream server directly:

```python
unenforced_direct = MultiServerMCPClient({
    "direct-downstream": {
        "transport": "stdio",
        "command": "/absolute/path/to/downstream-mcp-server",
        "args": ["--downstream-flag"],
    }
})
```

The disposable integration test proves that a tool blocked through Reconc
executes through this direct configuration, then verifies that both diagnostics
retain the unenforced classification. `reconc status . --json` therefore
reports `mcp_gateway_scope: "explicit_routes_only"`,
`mcp_external_configuration: "not_inspected"`, and
`mcp_bypass_routes: "unenforced"`; `reconc doctor . --deep` emits the same
boundary in its MCP diagnostic. Those reports never turn an uninspected direct
configuration into a safe or enforced claim.

The gateway also does not claim transparent prompts, resources, sampling,
roots, tasks, HTTP, SSE, or general framework interception. A pre-call block
prevents a routed tool from executing. Post-result containment can withhold
data from the model boundary but cannot undo a side effect that already
occurred.

The compiler lowers `actions.tools`, `actions.rules`, `actions.budgets`,
`actions.approvals`, `actions.detectors`, `actions.ledger`, and
`actions.defaults` plus compatible legacy `mcp` authoring into one canonical
format-6 action plan.
It rejects unknown nested fields, ambiguous values, invalid or oversized
predicates, duplicate ownership, incompatible defaults, and unsupported
cross-field combinations. Regexes, doublestar globs, CIDRs, URL/path
constraints, JSON Pointers, and typed constants are compiled once into the
immutable runtime plan. `reconc why action .` explains the result with operand
values redacted.

Canonical action values expose exact encoded size and bounded indexed reads
without revealing mutable collection storage. Pointer traversal and redacted
operand summaries therefore do not clone arrays, objects, or encoded JSON.
Compiled path predicates retain the normalized base, volume, case-folded form,
and descendant prefix; only the request operand is normalized while matching.
Scalar-list compilation encodes each canonical sort key once.

The pure evaluator produces exactly `allow`, `warn`, `require_approval`, and
`block` with
`block > require_approval > warn > allow` precedence. Arguments and trusted
context use strict typed JSON, exact RFC 6901 pointers, deterministic predicates,
explicit provenance, fail-closed bounds, redacted bounded traces, and complete
in-memory cache identities rather than an opaque numeric risk score. Cache reuse
requires exact request, transport, tool, plan, policy, context, principal,
credential, state, approval, taint, repository-effect, phase, deadline, and
lifecycle identity plus immediate resampling. Persistent low-entropy and
secret-adjacent identities use exact domain-separated keyed identities from an
operator-owned local key. Missing, malformed, wrongly permissioned, rotated, or
unleased key material makes the dependent identity unavailable.

The gateway prepares one immutable normalized evaluation and one exact cache
identity per logical request. Lookup, evaluation, and store share that binding;
store accepts a result only when its eligible identity matches exactly. The
standalone cache methods preserve their existing API and perform their own
preparation when no prepared request is supplied.

Apple M1 TASK-270 measurements moved 128 context-root predicates from 21,816
B/op and 259 allocations to 312 B/op and 3 allocations. Pointer traversal plus
summary remains at zero allocations through the maximum legal JSON depth. The
maximum legal action plan reduced allocations from 810 to 806 in the calibrated
run; prepared cache lookup and store each avoid normalization and retain only
the required defensive result copy.

Independent enforcement requires an operator-supplied expected lock digest.
An explicit repository-managed mode is available with lower provenance and a
visible policy-tampering boundary. Repository policy can never select the
downstream executable, argv, working directory, inherited
environment, credential material, state key, or approval authority.

The implemented inspection layer adds deterministic local argument, result, and
progress detectors, strict output-schema validation, unsupported-content
classification, and post-result withholding. The implemented ledger layer adds
typed payload-free events, selected-field keyed identities, private atomic
multi-process append, bounded rotation, crash recovery, archive and detached-head
verification, explicit lifecycle aggregation, exact Impact Lab ledger
assertions, and verified minimized export with explicit omissions. The
implemented `internal/actionevidence` layer builds deterministic local JSON or
Markdown from the current policy and lock identity, retained ledger integrity,
read-only budget state, reverified approval receipts, and exact Impact Lab
scenario results. Built-in versioned maps reference SOC 2, GDPR, the HIPAA
Security Rule, and the EU AI Act by control identifier and primary-source URL;
strict digest-pinned or Ed25519-signed custom maps may add selectors but cannot
set status or override evidence facts. Missing, invalid, stale, incomplete, or
out-of-window evidence is downgraded explicitly. This is bounded technical
evidence mapping only; organizational control operation, legal assessment, and
external assurance remain outside Reconc. The MCP stdio gateway is live only
for routed tools.
Key rotation cannot return budget capacity: it is serialized against live key
leases and refused while dependent action state exists unless a future explicit
atomic migration or reset owns every dependent identity and record.

The full proposed contract, exact resource limits, timeouts, failure matrix,
versioning rules, package ownership, and deterministic conformance vectors are
in
[RECONC-0008](rfcs/RECONC-0008-go-only-action-plane.md).

## Architecture

Pipeline:

```text
repo root -> ingest -> parser -> compiler -> .reconc/policy.lock.json -> runtime -> CheckReport/FixPlan/CompletionReport -> ProofBundle
                                  \-> in-memory candidate -> impactlab -> ImpactReport
                                  \-> action plan -> mcpgateway -> downstream stdio MCP tool
```

The exhaustive contributor package map is maintained in
[the architecture reference](architecture.md#package-map). The following list
summarizes the core runtime responsibilities:

- `cmd/reconc`: CLI entry point only
- `buildprovenance`: deterministic target/source build identity and byte-only binary inspection
- `internal/cli`: argument parsing and command dispatch
- `internal/action`: pure canonical action contract, strict normalized values with read-only indexed traversal, immutable matcher programs, deterministic evaluation with collection-time bounded redacted traces, and exact in-memory decision caching
- `internal/actionapproval`: canonical signed requests and receipts, operator authority registry, transport-neutral provider contract, and exact current input-required and legacy form-elicitation mappings
- `internal/actioninspect`: strict MCP result decoding, offline output-schema validation, concurrency-safe compiled detector packs, allocation-bounded content inspection, and safe withholding
- `internal/actionledger`: strict payload-free lifecycle events, private retained chain, crash recovery, exact lifecycle aggregation, and deterministic verification
- `internal/actionledgerexport`: verified synthetic minimized Impact Lab export with explicit omission and replay-completeness truth
- `internal/actionstate`: trusted context identities, key leases, cumulative budgets, atomic approval consumption, and crash-safe private state
- `internal/mcpgateway`: strict MCP framing and tool discovery, operator-bound child process ownership, live pre-dispatch enforcement, approval flow, progress/result inspection, lifecycle ledger completion, and bounded upstream delivery
- `internal/ingest`: repository discovery and one-root-handle stable source loading
- `internal/parser`: YAML-to-policy validation and normalization
- `internal/compiler`: canonical JSON lockfile generation, digesting, conflicts, migrations, compile lock
- `internal/impactlab`: strict format-2 repository/action replay corpora, exact reviewed action-delta gates, privacy and completeness checks, and deterministic current-versus-candidate comparison
- `internal/bootstrap`: deterministic canonical init plus inspect/plan/apply/verify/remove; hermetic read-only repository planning; digest-bound resolution, apply, durable recovery, and verification; portable/private receipts; repository locking; managed-block ownership; policy migration; and platform-bound binary resolution
- `harness` and `internal/harnesspack`: embedded advanced pack ownership, strict manifest/archive validation, compatibility, and byte parity
- `internal/usercli`: locked binary-plus-receipt installation, manager classification, exact PATH identity, global diagnostics, bounded release selection, atomic direct updates, package-manager delegation, and ownership-safe uninstall
- `internal/stackdetect`: shared bounded manifest/source stack discovery
- `internal/runtime`: strict lock trust, immutable typed and indexed policy plans, policy evaluation, remediation, git integration, scripts, templates
- `internal/schema`: typed per-artifact public JSON schema registry, immutable publication identities, compatibility aliases, digests, release assets, and enterprise URL resolution
- `internal/assurance`: bounded native repository assurance evaluators
- `internal/hooks`: typed hook platform and verification-surface registry, artifact generation, non-destructive install/uninstall, scaffold sync, managed activation, and diagnostics
- `internal/runtime/agentsession`: hook-runtime session state and event handling
- `internal/audit`: opt-in SHA-256-linked decision log, detached head, verification, and rotation
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
- `internal/safename`: strict lower-kebab identifiers for user-controlled asset names
- `internal/presets`: bundled and user policy packs
- `internal/templates`: bundled and user rule templates
- `internal/tasklifecycle`: typed TASK profiles, validation, bounded briefing,
  recoverable transactions
- `internal/tui`: dependency-free terminal dashboard

Key invariants:

- Deterministic JSON artifacts
- Stable schema and `format_version` fields; all 36 current and legacy contracts are registry-owned and ship under unique names, current artifact schemas span v1-v6, legacy portable policy locks use v1-v5, and current portable policy locks use v6
- Fail closed on malformed policy, stale lockfiles, schema drift, invalid globs, unsupported rule kinds, and non-portable current lock envelopes
- No core policy-runtime network calls; supported agent hosts own their
  authenticated inference traffic
- Behavior in internal packages, thin `cmd/reconc/main.go`

## Agent Skill

The repo ships one agent-facing skill at `skills/reconc/SKILL.md`.

It is written for Codex, OpenCode, Claude Code, Oh My Pi, Pi, ZCode, and other coding agents. The
skill documents the same reconc workflow for every agent runtime:

- begin and reenter with the versioned `session-briefing --json` contract
- collect truthful read, write, command, and claim evidence
- use `reconc next .` for remediation
- run `reconc done .` before claiming completion
- distinguish native hook enforcement from CLI self-checks

The typed platform registry is the source of truth for Git pre-commit, Claude
Code, Codex, GitHub Copilot, Cursor, OpenCode, Devin CLI, Antigravity CLI,
Kilo Code, Oh My Pi, Pi, ZCode, Grok Build, and Kimi Code CLI. It owns native event names,
normalized lifecycle coverage, compatibility routes, config and scaffold paths,
failure behavior, timeout budgets, output budgets, installation strategy, and
activation probes. `reconc hook status
[repo] [--json]` validates every registered artifact and reports `absent`,
`installed`, `configured`, `degraded`, `shadowed`, or `unsupported`.
`configured` means the static configuration is complete and host-discoverable;
it is not proof that a host process executed it. JSON `surface_events` reports
the registry's per-surface documented route sets. Separate `expected_events`,
`live_events`, `unseen_events`, `last_seen`, and `last_event` fields report
the complete artifact routes and which ones a live runtime actually executed.
Liveness is stored outside the repository and each route writes at most once
every six hours.
Status remediation is derived from an internal typed disposition and exact
program-plus-argv data, never from diagnostic prose. A normal install, a
managed forced repair, a user-owned conflict, a host-specific action, and no
action remain distinct. `--force` is emitted only when the installer requires
and can safely apply it without replacing an unrecognized shared wrapper;
foreign targets and ambiguous legacy duplicates receive manual ownership
guidance. Commands render as dynamically fenced POSIX-sh or PowerShell syntax,
so repository paths containing whitespace, quotes, shell metacharacters,
backticks, backslashes, or newlines remain literal copyable arguments without
breaking Markdown. Windows rendering uses `ProcessStartInfo` plus the native
Windows command-line escaping contract, avoiding Windows PowerShell 5.1's
lossy direct native-command argument binder while preserving empty arguments.
Human output keeps only the seen/expected count and last event so large route
registries do not dominate the terminal. A route counts as installed only when
the artifact carries it as a complete token: many routes are prefixes of a
sibling, for example `claude-stop` of `claude-stop-failure` and every
`<platform>-post-tool-use` of its `-failure` variant, so a text match would
hide the shorter route instead of reporting it missing.

Merge ownership is decided on what an entry executes, not on the wrapper path
appearing somewhere in its text. A user hook that only names
`tools/reconc/bin/hook` in an argument or a message stays user-owned and is
preserved, while any executable inside `tools/reconc/bin/`, including a renamed
wrapper, remains a Reconc entry that install and uninstall refuse to treat as
foreign. Current generated shell resolvers and the former login-shell resolver
are recognized only by reconstructing their complete byte-exact template;
unparseable commands and marker text alone remain user-owned. Standalone
plugins and hook files likewise require the format-defined exact first-line,
top-level object, or parsed-command signature before Reconc may replace them.

Hook output that exceeds a route's byte budget still delivers a decision, on
the channel that route's host reads. Cursor, GitHub Copilot, and Grok express
deny and block as exit code 0 plus a JSON body, so an oversized fail-closed
result is replaced by the smallest valid envelope of that same shape and keeps
exit code 0; emptying stdout there would hand the host an undecided non-zero
exit, and on GitHub Copilot it would additionally re-trigger the installed
missing-binary fallback so two decision bodies arrive. Routes whose decision
travels in the exit code keep the empty-stdout, exit-code-2 shape, which is
also the fallback whenever no envelope fits the budget. Fail-open routes stay
exit code 0. In every case the bounded stderr keeps the runtime's own reason
when it has one and appends the byte-budget notice, because the oversized
stream is stdout and the operator needs the cause, not only the symptom.

Repository-owned third-party adapters live only in
`.reconc/runtimes/<name>.json`. Each manifest is a bounded non-symlink regular
JSON file with a reserved-safe `custom:<name>` identity, sorted exact host
routes, explicit timeout/output/failure budgets, host guarantees, and RFC 6901
field mappings. Reconc executes no manifest-supplied code, expression, shell,
template, or network action. The compiler validates the filename and manifest,
adds its digest and redacted capability summary to the lock contract, and
therefore makes any manifest edit stale until explicit refresh.

`reconc hook bridge <name> <host-event> [repo]` reads one bounded host payload,
strictly validates its 8 MiB byte, 32-level depth, 65,536-member,
65,536-item, 13-mapping, and 2 MiB retained-selected-value budgets, then walks
one trie for all declared pointers. Duplicate names, invalid UTF-8 or numbers,
malformed structure, and trailing data fail closed. Unselected subtrees use
`SkipValue` and never become a generic interface tree. Reconc copies only the
selected neutral fields, checks the fresh compiled identity, reuses
the existing session/policy/MCP/Stop engine, emits one bounded versioned JSON
response, and records route liveness. Routes lacking pre-execution,
synchronous-response, authoritative-outcome, continuation, continuation
acknowledgement, or exact MCP identity guarantees return `unsupported` and do
not execute an enforcing handler. `reconc hook conform <manifest> <fixtures>`
validates request, response, timeout, failure, liveness, and privacy fixtures
offline. Generic local-agent and CI-bot fixtures ship as executable contract
tests; built-in adapters remain registry-owned.

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
`surface_events`, `expected_events`, `live_events`, `unseen_events`,
`last_seen`, and `last_event` fields keep documented surface eligibility,
complete artifact coverage, and live truth separate. Its `observations` map
exposes only bounded, source-free metadata for observational routes; OMP
`user_python` reports count, latest timestamp, repository-relative working
directory, code byte size, and context-exclusion flag. `reconc hook verify`
owns the same registry-derived matrix. Its default offline mode creates a
disposable repository and separately verifies artifact generation,
configuration, generated wrapper or Bun-adapter transport, a real synthetic
policy decision, native response adaptation, and duration. It never invokes a
host, model, account, cloud service, or the caller's repository. Offline
verification discovers the POSIX `sh` transport on native Windows, including
Git for Windows, and normalizes generated Bun module paths with file URLs; an
absent shell remains explicitly incomplete rather than passing by inspection.
The disposable repository and homes are installed in a dedicated child process
with an explicit environment, so concurrent verification cannot temporarily
mutate or incorrectly restore the parent process environment.
The offline `synthetic_enforced` fact is not promoted to `loaded`, `observed`,
or live `enforced`; all expected host routes remain explicitly unproven.

The explicit `--live --host KIND --surface SURFACE --allow-authenticated` mode
prepares one disposable host exercise and waits for the operator without
launching or authenticating the host. Its temporary shim records only route
identity, sorted top-level field names, result class, exit code, and duration;
it never writes raw payloads, prompts, tool arguments, output, secrets, or
repository content. Missing delivery, partial matrices, operator EOF, absent
negative enforcement, unsupported direct transports, unavailable tools, and
missing executables for known local host surfaces stay degraded or unproven.
Executable availability is reported only for surfaces with an exact local
discovery contract; UI- and cloud-only surfaces are not guessed.
`scripts/tests/host-integration-probe.sh` delegates to that product command and
contains no second matrix.

| Surface | Project artifact and eligible contract | Strongest truthful guarantee before a live probe |
| --- | --- | --- |
| Cursor desktop Agent | `.cursor/hooks.json`; installed Agent lifecycle, tool, shell, MCP, subagent, compaction, Stop, and sessionless workspace routes | `configured` and `discoverable`; each route becomes `observed` or `enforced` independently |
| Cursor desktop Cmd+K | The same Agent-hook entries plus sessionless `workspaceOpen` when Cursor emits the corresponding lifecycle | Shared Reconc route semantics, not blanket Agent parity |
| Cursor inline Tab | `afterTabFileEdit` only; read-prevention and Agent lifecycle are intentionally absent | Successful Tab-write evidence only after that exact route is observed |
| Cursor CLI interactive | The same project file under `agent`; documented routes are session start/end, prompt decision, generic pre/post tool, Stop, and sessionless workspace liveness | Registry-derived eligibility only; no IDE/CLI parity claim without event-by-event live evidence |
| Cursor CLI print mode | The same project file under `agent --print`; documented route set matches interactive CLI | No interactive/print delivery claim; structured CLI output is not substituted for missing pre-action hooks |
| Cursor cloud agents | Repository hooks after a writable environment exists; session start/end, dedicated MCP, and Tab routes are unavailable | Only documented eligible routes; no live claim without approved cloud execution |
| OpenCode CLI | `.opencode/plugins/reconc.js`; prompt, permission, tool, session, compaction, terminal failure, and inferred idle continuation | Static plugin contract plus per-route liveness; continuation remains inferred |
| Kilo Code CLI | `.kilo/plugin/reconc.js` with `KILO_PURE` unset; same lifecycle classes as OpenCode | Static plugin contract plus per-route liveness; continuation remains inferred |
| Kilo Code VS Code host | The same canonical project plugin when that host loads external project plugins | CLI observations are never reused as VS Code proof |
| Oh My Pi CLI | `.omp/extensions/reconc.ts`; native session, input, tool, user-shell, user-Python observation, approval, compaction, shutdown, and awaited main-session Stop routes | Static extension contract plus per-route liveness; `tool_call`, `user_bash`, and `session_stop` can enforce before host action, while `user_python` is observed and never decided |
| Pi Coding Agent | `.pi/extensions/reconc.ts`; trusted-project session, input, tool, user-shell, result, compaction, settled, and shutdown routes | Static extension and saved-trust contract plus per-route liveness; `tool_call` and `user_bash` can enforce before host action, while settled continuation remains inferred |
| ZCode CLI | `.zcode/config.json`; all seven native session, prompt, tool, permission, failure, and synchronous Stop routes through the documented process executor | Static workspace contract plus per-route liveness; pre-tool, permission, and Stop can block, while host timeouts remain fail-open |
| Kimi Code CLI | User-global `$KIMI_CODE_HOME/config.toml`; the 16 decision- and evidence-carrying hooks of the host's twenty dispatch through bare `reconc` and discover the current repository | Generator-exact global configuration only; no live claim without a real Kimi route observation |

Cursor's registry classifies all 21 current host events exactly once. Reconc
installs 17: `sessionStart`, `sessionEnd`, `preToolUse`, `postToolUse`,
`postToolUseFailure`, `subagentStart`, `subagentStop`,
`beforeShellExecution`, passive `afterShellExecution`,
`beforeMCPExecution`, `afterMCPExecution`, `afterFileEdit`,
`beforeSubmitPrompt`, `preCompact`, `stop`, `afterTabFileEdit`, and
`workspaceOpen`.
`beforeReadFile` and `beforeTabFileRead` cannot prove successful reads;
`afterAgentResponse` and `afterAgentThought` expand a privacy-sensitive,
non-evidentiary surface. Those four events remain explicit unsupported
dispositions and are not installed as no-ops. `workspaceOpen` is a
sessionless app-lifecycle route: Reconc validates and redacts its documented
payload, records only route liveness, creates no session or repository
evidence, and returns `{}` without plugin paths.

Cursor records positive generic tool evidence only from `postToolUse`.
`postToolUseFailure` records failure without positive read, write, or command
evidence. `afterShellExecution` contains output and duration but no
authoritative exit status, so it records liveness only. `afterFileEdit` and
`afterTabFileEdit` are successful write fallbacks deduplicated against generic
tool delivery. Tool and subagent decisions return `permission`; prompt
submission returns `continue`; observation and workspace routes return `{}`.
Stop and subagent Stop use Cursor's bounded `followup_message` response. A
malformed or outcome-unknown post event cannot satisfy command freshness,
completion, or proof.

The CLI probe prefers the official `agent` command and accepts
`cursor-agent` only as a backward-compatible alias. It verifies the help
contract before treating either executable as Cursor, so an unrelated `agent`
binary cannot create a false host claim. Cursor's confirmed `AskQuestion`
host bug currently emits none of the generic pre/post tool hooks in IDE or
CLI; Reconc cannot reconstruct that missing pre-action boundary. Cursor has
also reported host-side `subagentStart` deny enforcement gaps. These are host
limitations, not adapter parity, and remain outside strict Reconc guarantees:
`https://forum.cursor.com/t/cursor-cli-askquestion-tool-skips-pretooluse-and-posttooluse-hooks/161836/6`
and
`https://forum.cursor.com/t/subagentstart-hook-deny-is-not-enforced/166143/4`.

Kimi Code CLI reads hooks from the user-global
`$KIMI_CODE_HOME/config.toml`, not repository-local `.kimi-code/local.toml`.
`reconc hook install kimi-code` uses a cross-process lock and atomically
merges one marker-owned TOML block containing all 16 documented events:
`SessionStart`, `SessionEnd`, `UserPromptSubmit`, `PreToolUse`,
`PostToolUse`, `PostToolUseFailure`, `PermissionRequest`,
`PermissionResult`, `Stop`, `StopFailure`, `Interrupt`, `SubagentStart`,
`SubagentStop`, `PreCompact`, `PostCompact`, and `Notification`. It preserves
all unrelated bytes and the existing file mode, refuses invalid TOML or
managed-block drift, and creates an exact private backup before a forced block
replacement. Uninstall removes only a generator-exact managed block.

Each global Kimi command uses bare `reconc`, starts in the host-supplied
project working directory, discovers an explicit Reconc configuration, and
silently returns outside initialized Reconc repositories. The adapter strictly
validates snake_case payload identity, session, current working directory,
tool input, and error shape before entering the shared runtime. Kimi accepts
exit code 2 as the blocking result for `PreToolUse`, `UserPromptSubmit`, and
`Stop`; exit zero allows, while every other non-zero exit, crash, and timeout
is host-fail-open. Post-tool text has no authoritative exit status and never
becomes positive command-success evidence by inference. Kimi is not installed
or launched by Reconc tests; isolated temporary `KIMI_CODE_HOME` fixtures prove
generation, merge, drift refusal, dispatch, and removal.

Oh My Pi loads the generated project extension from
`.omp/extensions/reconc.ts`. `reconc hook install omp` creates that exact
marker-owned file and refuses to replace foreign content, including with
`--force`; uninstall removes only the generator-exact managed extension. The
extension registers the current typed `ExtensionAPI` routes for session start,
user input, pre-tool, post-tool, tool failure, approval requested/resolved,
pre/post-compaction, awaited main-session Stop, and session shutdown. OMP task
sessions do not emit `session_stop`, so continuation is deliberately scoped to
the main session and capped at eight accepted continuations per session.

OMP `tool_call` and `session_stop` are blocking boundaries. A deny decision,
malformed decision, Reconc failure, or timeout fails closed in the host-native
response contract. A host-aborted Stop yields immediately without starting a
continuation. Approval, post-tool, compaction, and shutdown routes are
observational and fail open after bounded diagnostics. `tool_result`
uses the host's exact `isError` outcome; only a successful built-in `Bash` call
without an explicit exit status receives synthetic exit code zero. Tool output
never decides success. The adapter drains stdout and stderr concurrently under
one 8 KiB budget, rejects invalid UTF-8, kills and awaits timed-out subprocesses,
and gives shutdown observation one second inside OMP's two-second handler
budget. OMP's installed runtime and source declarations are not live hook proof;
only exact per-route liveness or an isolated negative probe can establish
observation or enforcement.

Pi discovers project extensions under `.pi/extensions/` only after project
trust. `reconc hook install pi` owns exactly `.pi/extensions/reconc.ts`, never
replaces foreign content, and never mutates `~/.pi/agent/trust.json` or the Pi
settings file. Status resolves `PI_CODING_AGENT_DIR`, canonicalizes the
repository root, applies nearest-parent saved trust, and accepts
`defaultProjectTrust: "always"` as the other persistent configured state. Both
Pi files are read through the bounded discovery contract: special files such as
FIFOs are refused instead of blocking status, the file identity must not change
between the size check and the read, and a final symlink is followed on purpose
because these are user-owned Pi configs that dotfile managers commonly link.
Interactive trust or `pi --approve` can activate one run but does not become a
static saved-trust claim.

The Pi adapter registers `session_start`, `input`, `tool_call`, `tool_result`,
`user_bash`, `session_before_compact`, `session_compact`, `agent_settled`, and
`session_shutdown`. Awaited `tool_call` and `user_bash` are fail-closed before
host execution. An allowed `user_bash` returns no replacement result and lets
Pi execute its own command; a denial returns a complete synthetic shell result
with exit code 2. Pi exposes no post-user-shell event. `tool_result.isError` is
authoritative, and only a successful built-in `Bash` result receives synthetic
exit code zero. Failed output never becomes an inferred exit status.

Pi's only veto of that shape is `project_trust`, which decides whether the
project is trusted at all rather than whether one tool call is permitted;
Reconc reads that saved decision instead of casting a vote in it. Pi therefore
exposes no per-call permission event, MCP discriminator, synchronous Stop event,
or continuation delivery acknowledgement. Reconc therefore maps permission
and MCP policy only through the generic pre-tool identity, and maps
`agent_settled` to a fail-open bounded continuation request. State is capped at
1,024 sessions and ten requested continuations per session; duplicate settled
events and injected Reconc input are suppressed by generation and in-flight
state. `sendUserMessage` returning only `void` means the adapter reports
requested, failed-before-call, or suppressed delivery without claiming host
acceptance. Host cancellation releases immediately. Every registered runtime additionally carries a host event record: the event
vocabulary that host accepts, transcribed from its own published reference or
source, with the location, the revision where one exists, and the date it was
taken. A test compares each record against the registry in both directions, so
a route the host does not publish and an event the host publishes that Reconc
drops both fail. Every deliberate non-binding carries its reason in the record,
which is how "the host cannot do this" stays distinguishable from "Reconc
chooses not to". Contract fixtures pin Pi
source revision `ac4ac9eaf69f2b01ca3af984a5c48f3b99b84278` at
`@earendil-works/pi-coding-agent` v0.84.1 and OMP revision
`06343fef4200c4e32d18f08df5a6a8bd84dcc710` at v17.2.4. That Pi revision widened
the blocking tool result with `terminate`, a hint the host honors only when
every finalized call in a tool batch sets it. Reconc has no policy mode that
ends a session, so the adapter keeps returning `{block, reason}` and leaves the
hint unused.

ZCode snapshots workspace configuration from `.zcode/config.json` when a
session starts. `reconc hook install zcode` adds `hooks.enabled=true` only when
that field is absent and preserves an explicit user `false`, which status
reports as installed but disabled. It merges the seven generated event groups
under `hooks.events` while preserving unrelated top-level keys, hook settings,
and foreign event entries. An
incompatible `hooks` or `hooks.events` shape fails closed; `--force` first
publishes a private content-addressed backup and then repairs only the invalid
hook subtree. Repeated install is byte-stable. Uninstall removes only exact
Reconc process entries and refuses a modified Reconc-looking entry without
mutating the file.

The process executor invokes `sh` with explicit argv
`tools/reconc/bin/hook`, the ZCode route, and `.`. Status requires the shared
wrapper, all generated entries, and `sh` on `PATH`; explicit
`hooks.enabled=false` is valid installed-but-disabled state.
ZCode exposes `SessionStart`, `UserPromptSubmit`, `PreToolUse`,
`PermissionRequest`, `PostToolUse`, `PostToolUseFailure`, and `Stop` as
one-line snake_case JSON envelopes. Hard pre-tool blocks use exit code 2,
permission denials use the native `hookSpecificOutput.decision` object, and
Stop continuation uses `decision: "block"`; malformed fail-closed requests use
the native exit-code-2 shortcut. Observation errors and all host timeouts are
fail-open. Stop can request at most three consecutive continuations. ZCode has
no native SessionEnd or post-compaction event, and Reconc reports both as
unsupported rather than inventing compatibility routes.

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
    - platform: omp
      tool: mcp_repository_read
      effect: repository_read
      path_fields: [/path]
    - platform: pi
      tool: deploy_preview
      effect: external
    - platform: zcode
      tool: Write
      effect: repository_write
      path_fields: [/file_path]
    - platform: claude-code
      tool: mcp__filesystem__write_file
      effect: repository_write
      path_fields: [/path]
    - platform: codex
      tool: mcp__filesystem__write_file
      effect: repository_write
      path_fields: [/path]
```

Each mapping is an exact `(platform, server_fingerprint, tool)` selector.
Fingerprint presence is part of identity: a fingerprinted call never falls
back to an unqualified mapping, and an unqualified call never matches a
fingerprinted mapping. `path_fields` and `command_field` are exact RFC 6901
JSON Pointers. Repository paths must resolve inside the target repository;
missing, malformed, escaping, or wrong-typed values become unclassified and
produce no positive evidence. `external` calls never become repository
evidence.

Cursor's dedicated MCP pre-hook can enforce `unclassified: deny`. OpenCode,
Kilo, OMP, Pi, and ZCode expose exact generic tool identities but no reliable discriminator
between an unconfigured MCP tool and a built-in/custom tool, so strict
unclassified deny is unavailable on those generic surfaces. Configured exact
identities remain enforceable.

Claude Code and Codex publish no dedicated MCP event, but both name every MCP
call `mcp__<server>__<tool>` and both accept a regular-expression matcher.
Reconc installs that namespace as its own matcher group on the generic tool
events and routes it into the MCP path, so the exact selector is the identity
the host itself uses. The namespace is the discriminator those generic surfaces
lack, which makes `unclassified: deny` enforceable before execution on both
hosts. Only the identity and the arguments continue past normalization; a
payload that reaches the namespace route under a built-in tool name is envelope
drift and fails closed. A completed call is still not a successful one: positive
MCP evidence requires an explicit host success result. Server locators, credentials, arguments,
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
the managed artifact is published. Merge-based installers retain the bounded
source snapshot used to build the result and revalidate its exact bytes,
filesystem identity, mode, size, and modification time immediately before
atomic publication. A concurrent edit or same-byte replacement is rejected.
Hook-install JSON always exposes an explicit `success` boolean; partial failure
also retains the partial report and emits its error without claiming success.

The registry assigns 5-second observation/session budgets, 10-second pre-tool
and permission budgets, and platform-specific Stop budgets instead of one
blanket timeout. Claude, Codex, GitHub Copilot, Cursor, Devin, Antigravity, Grok,
and ZCode generators emit those host timeouts; OpenCode, Kilo Code, OMP, and Pi
enforce them inside their adapters. OMP uses a 29-second internal Stop budget
so its fail-closed response is returned before the host's 30-second
extension-handler deadline.
Each runtime route caps combined process output at 8 KiB.
Post-compaction recovery context is deduplicated and capped at 4 KiB.
OpenCode, Kilo Code, OMP, and Pi keep one repository-owned `reconc hook worker`
child for the lifetime of their plugin instance. Format-1 newline-framed JSON
requests carry bounded IDs, event, repository, and payload fields and are
processed in deterministic order. Cancellation or route timeout kills the
child; startup, crash, or protocol failure uses the remaining route budget for
the existing one-shot path. Protocol drift disables reuse for that plugin
instance, while a later plugin instance picks up an installed binary upgrade.
Shutdown closes the worker, and stdin EOF prevents orphans if the host exits.
The worker reuses a revalidated operating-system repository identity and an
immutable typed policy plan. Resolution eagerly freezes the filesystem object
ID before caching, including Go's otherwise lazy Windows file ID, so replacing
a repository at the same lexical path invalidates the cached handle. Each request
still reads bounded lock bytes and the complete source bundle identity:
lock-byte drift rebuilds the plan, while source drift invalidates it and fails
closed. A cached plan owns the sorted, unique, validated include-pattern recipe
and its derived glob bases from the full source load. Stable freshness checks
therefore do not reread or decode compiler configuration just to recover
includes, and they reuse ingest's bounded include expander instead of
materializing an unbounded filesystem glob. They still hash both config
candidates and all discovery markers,
sources, relevant directories, presets/global state, and custom runtimes; any
config identity or content change rejects the recipe before a full source load
decodes the new configuration. Session and taint inputs remain freshly loaded.
No daemon, socket, listener, or runtime network call is added.
Oversized newline frames are discarded with bounded retained memory and a
bounded drain budget. A fully terminated oversized frame gets one deterministic
protocol error and the same worker continues with the next frame; missing
terminators or drain-budget exhaustion terminate the worker, preserving the
existing one-shot fallback only for genuine protocol loss.
Response bytes are accumulated in the generated adapter with geometric growth,
so one-byte or irregular pipe chunks perform linear total copying. After a
response, only the unread remainder survives and the buffer remains bounded by
the 128 KiB response limit.
Concurrent session updates keep the unchanged active-session pointer on a
lock-free read fast path. If that optimistic read overlaps an atomic Windows
publication, Reconc rechecks once under the existing active-session lock;
persistent read or validation failures still fail closed.

Claude Code, Codex, GitHub Copilot, Cursor, Devin, Antigravity, Grok, and ZCode
generated repository configs use `tools/reconc/bin/hook` on POSIX; the wrapper
owns repo-local binary selection and PATH `reconc` as last fallback. Kimi Code
is global and therefore invokes bare `reconc` directly after explicit install
verifies that PATH identity.
For development and self-hosting, the wrapper checks `.build/bin/reconc` and
root `reconc` before invoking any platform probe. A repository installation
that owns both the wrapper and an exact current-host stable binary also owns
`tools/reconc/bin/hook-target`. The wrapper reads that one-line receipt with a
shell builtin, accepts only the five supported stable repository paths, and
executes the regular, non-symlink, executable target without running `uname`,
scanning a directory, expanding a version glob, or searching `PATH`. A missing,
invalid, symlinked, or non-executable direct target enters the portable recovery
resolver. That resolver probes the host, prefers the stable platform name in
`tools/reconc/dist` and root `dist`, and accepts exactly one compatible
versioned artifact as a migration fallback. Multiple compatible versions fail
closed before PATH fallback. Transactional bootstrap, repository sync,
rollback, and removal own the receipt as `hook-wrapper-target` together with
the wrapper and binary. Cross-platform plans omit it instead of publishing a
target that cannot execute on the current host.
The development binaries and all repository-local release binaries remain
writable by the repository owner and are not re-attested on every hook event.
This resolution order is a convenience and availability contract inside the
documented non-hostile same-user model, not a security boundary against a
process that can replace repository files. Use a sandbox and protected remote
CI outside that write authority when hostile same-user replacement is in scope.
Claude Code uses its exec-form
`command`+`args` shape so it does not spawn a hook shell or run a hook-launcher
Git lookup. Codex uses the host shell command string without a nested launcher;
Cursor and Antigravity use portable non-login `sh -c` launchers with a direct
wrapper fast path before their Git fallback.
Codex bootstrap and direct hook installation manage `hooks = true` under the
`[features]` table or the equivalent root dotted key
`features.hooks = true`. The shared bounded reader respects quoted `#`
characters and rejects duplicate, root-level, or misplaced declarations.
Direct installation rejects an explicit user-owned
`hooks = false` before any artifact write unless `--force` is supplied.
Transactional bootstrap reports the change as managed drift and requires the
explicit marker-only acceptance path. Forced or accepted activation records
the exact original line inside the managed block; hook uninstall and bootstrap
removal restore that line byte-for-byte. A root-level `hooks=true` lookalike is
invalid. Codex accepts `SessionEnd` among the eleven matcher groups its hook
configuration defines, so Reconc routes it like every other host that publishes
the event. Reconc generates only supported routes and gives each route its
exact 5, 10, or 30 second host timeout. Codex also has no
separate failed-tool event: Reconc classifies non-successful Bash outcomes from
the released `PostToolUse` payload and records them through the failure path.
User prompts, pre/post compaction, subagent start/stop, permission, tool, and
Stop lifecycles are all routed. `apply_patch` is routed through Reconc by
parsing patch headers from `tool_input.command`; a non-empty patch with zero
parseable file operations fails closed instead of silently bypassing the write
gate. The same rule covers every registered write tool: a payload that names a
write tool but carries no extractable file path is envelope drift, and passing
it through would skip `deny_write` and `require_read` entirely, so it is
refused rather than admitted. GitHub Copilot uses the version-1 repository hook contract at
`.github/hooks/reconc.json`. Copilot CLI and coding agent load the same
repository file, while the coding agent honors only its Linux `bash` command
in `/workspace`. Reconc generates documented PascalCase compatibility events
plus native `subagentStart`, validates `cwd` against the selected repository,
normalizes `tool_result` into evidence, and translates PreToolUse,
PermissionRequest, PostToolUseFailure, Stop, and SubagentStop output into
Copilot's exact schemas. `PermissionRequest` and `Notification` are CLI-only; cloud permission
enforcement therefore uses `PreToolUse`. The published hooks reference names
fourteen events. Reconc binds the twelve it can decide or attribute and leaves
`errorOccurred` and `userPromptTransformed` unbound; `PostCompact` does not
exist on this host and is reported as unsupported rather than generated. Copilot reads deny and block as exit
code 0 plus JSON, so a missing or failed wrapper on `PreToolUse`,
`PermissionRequest`, `Stop`, and `SubagentStop` emits that platform's explicit
deny or block envelope from the generated bash and PowerShell commands instead
of a bare non-zero exit; Copilot's own timeout behavior remains fail-open. The
managed filename is never overwritten when it contains foreign content, even
with `--force`. Static configuration and contract tests are not live proof;
only per-route liveness in `reconc hook status . --json` can establish that a
host actually executed the adapter. Cursor uses `.cursor/hooks.json` with the
registry-driven, surface-specific outcome contract defined in
[Host Integration Truth](#host-integration-truth). If Cursor also
executes compatible `.claude/settings.json` hooks, Reconc detects Cursor-native
payload markers and no-ops those non-native Claude hook invocations before they
can duplicate Cursor session evidence. Claude routes its native prompt,
permission-denied, failed-tool, Stop-failure, notification, subagent,
pre/post-compaction, and session events. Claude Code accepts 31 hook events.
Reconc installs the 15 that carry a policy decision or authoritative session
evidence, plus the `SessionStart` `compact` recovery matcher. The remaining
events stay unbound on purpose: they carry no decision Reconc can make and no
evidence it can attribute. `FileChanged` is the closest miss, because it
reports mutations from any source, including the user's own editor, so it
cannot establish that an agent session wrote a path. On top of those event
keys, Claude's tool events carry a second matcher group for the `mcp__.*`
namespace, which is how MCP calls reach the MCP policy path. After compaction, the context-capable `SessionStart`
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
Oh My Pi uses the typed Bun extension at `.omp/extensions/reconc.ts`. It
registers native `session_start`, `input`, `tool_call`, `tool_result`,
`approval_requested`, `approval_resolved`, `auto_compaction_start`,
`auto_compaction_end`, `session_stop`, and `session_shutdown` handlers. The
extension translates only host envelopes and decisions; policy and durable
session state stay in Go. On Windows it uses the same `sh` wrapper boundary.
Pi uses the typed Bun extension at `.pi/extensions/reconc.ts`. It translates
the nine native events and host decisions only; policy and durable session
state stay in Go. Its adapter shares the bounded output, UTF-8, timeout, kill,
await, wrapper-resolution, and Windows `sh` transport contract, while preserving
the host abort signal as authoritative cancellation.
ZCode uses `.zcode/config.json` instead of a generated TypeScript adapter. Its
native `process` entries call the same repository wrapper with explicit argv,
5/10/30-second event-specific `timeoutMs` values, and `*` matchers only on
tool-bearing events. The Go normalizer validates route/event identity,
repository cwd, session, stable tool-call identity, object-shaped tool input,
authoritative post-tool success/failure fields, and Stop reentry state before
dispatch. Generated configuration and native response shapes are pinned by an
official-contract fixture; live host execution remains a separate liveness
claim.
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

When the installed Grok guide documents passive Stop, optional leader mode
(`grok --leader`, config
`use_leader`, or `GROK_LEADER_SOCKET`) supplies backward-compatible TUI
continuation through protocol 1 `_x.ai/interject` over Unix sockets or Windows
named pipes. Reconc suppresses the interjection only when native Stop
capability is explicitly advertised, preventing duplicate prompts without
creating a version-based enforcement gap. The 32-attempt leader cap counts
only delivered interjections in one no-progress series; a changed material
session-event snapshot or a clean Stop resets it. Diagnostic and
block-reason wording changes do not count as progress. Every spawned child gets
exactly one `RECONC_GROK_STEER=0` entry after inherited duplicates are removed.
Multiple endpoints divide the
three-second budget fairly and framed messages complete short writes.
`RECONC_GROK_STEER=0` disables only leader steering; PreToolUse remains hard
while native Stop remains dependent on the installed host capability. Deep
doctor reports installed native Stop capability and separately probes optional
leader protocol plus `_x.ai/interject` with a random nonexistent session.
`reconc run on|off|reset|status|log` is the canonical AI-operated repository switch.
Its durable state applies only to the selected repository, not the whole machine.
Repository mode persists across sessions for Claude Code, Codex, GitHub
Copilot, Cursor, OpenCode, Devin CLI, Antigravity CLI, Kilo Code, Oh My Pi, Pi,
ZCode, Grok Build, and Kimi Code CLI. The agent
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
never prompt text. Repeated equivalent continuations persist only the initial
observation, material progress, the third and fifth unchanged nudge, and the
no-progress release; terminal and policy-checkpoint transitions are always
durable. The live log and two archives are each bounded at 2 MiB; readers merge
the ring in chronological order.
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
content/index hashes, policy-source identity, typed TASK state, direct
loose/packed/worktree HEAD resolution, and a
per-session report lock instead of full `git diff --binary` output or repeated
status walks. Packed refs are scanned line by line from the single bounded read
without materializing every unrelated ref as a string. The same bounded status snapshot scopes Stop-time write evidence
to paths that are both session-recorded and still uncommitted; unknown Git or
path state keeps the full session write set and therefore fails closed. The
completed report is cached under that initial fingerprint and the exact
read/write/command/claim evidence hash. One-shot hooks, small dirty states, and
all uncertain inputs rebuild that exact fingerprint. A persistent session
worker may reuse a fully published report for a dirty state of at least 16 MiB
or 1,024 entries after memory-only generation samples match canonical root,
Git status/HEAD/index, platform file identity and change time, recursive
untracked-tree metadata, policy lock and sources, every reachable
policy-declared input, typed TASK state, configuration, and session evidence.
The cache owns at most 64
repository/session entries and starts no watcher or background process.
Equivalent concurrent Stops serialize under the report lock and load complete
session evidence once before evaluation. Persistent workers retain at most
16 MiB of decoded, digest-bound evidence-segment prefixes across 64 keys. Every
reuse still rereads bounded regular files and rechecks exact bytes, identity,
metadata, segment count, chain head, linkage, and the newest state revision;
replacement, append, corruption, or drift invalidates reuse. Generation state is sampled around report
loading and revalidated after the final evidence reload. If evidence or an
exact cache input changes during policy evaluation, Stop re-evaluates current
state up to three times and then fails closed instead of returning or warming a
stale report. Re-entrant `stop_hook_active=true` calls apply the same final
revalidation before reusing a clean report.

Report reuse additionally binds the repository paths the compiled policy names,
including the `require_script` target itself.
`git status` never lists ignored files, so a gitignored `require_evidence` or
`require_fresh_file` target could otherwise be rewritten or deleted without
moving any fingerprint field. Every path a rule or composite check names,
contributes its supported cache identity to the fingerprint,
even when Git also reports that path as dirty. This separate identity is
required because Git describes a symlink object while a policy input may
follow a contained symlink to its target. A path that only exists after
template substitution makes Stop-report reuse non-cacheable. Completion remains
stricter: valid captures are resolved against the exact candidate write paths
with the evaluator's first-match semantics, and every resulting concrete target
is bound before and after evaluation. Malformed, unmatched, or untrusted
dynamic input fails closed. A lockfile that cannot be read or decoded is
likewise non-cacheable. The eligibility scan decodes the lockfile rather than
matching text. Within one Stop attempt, that bounded scan is shared by
fingerprinting, cacheability, generation capture, expiry, assurance inputs,
and cache storage through an attempt-local identity keyed by normalized write
paths and the exact lockfile SHA-256. A final bounded byte-hash comparison
invalidates the attempt if the lock changes, so no stale scan can authorize a
report reuse or generation entry.
The attempt also keeps complete typed before/evaluation/after identity
snapshots for Git, TASK state, policy sources, session evidence, and report
bindings; equality checks compare those phase snapshots rather than mixing
individual fields captured at different times.

A stored report additionally carries the instant its own inputs stop describing
it. `require_fresh_file` can turn a clean report stale from wall-clock time
alone, so the report expires at the earliest `modification time +
max_age_hours` across the age requirements, and both the session-state and the
persistent-worker warm paths re-evaluate past that instant. A policy without
age requirements carries no expiry and never ages out on time alone.

Only the gates a Stop can actually trigger decide whether its report may be
reused: a `require_script` rule whose `when_paths` match none of the session's
write paths runs no script, so it neither contributes to the report nor blocks
its reuse. A rule with no `when_paths` is triggerable. Valid templated patterns
are matched exactly against the session write paths; only malformed patterns
fail toward triggerable.

An applicable `require_assurance` rule is never report-cacheable. Native
assurance may inspect complete globbed authority surfaces and wall-clock-aged
proof records, which cannot be represented by the fixed path identity set used
by Stop caching. It therefore evaluates on every applicable Stop. Completion
additionally hashes the bounded native evaluation's exact loaded bodies,
directory observations, applicability results, derived facts, and
time-dependent findings before and after the policy decision, so a moving
assurance authority surface cannot certify a stale candidate.

A `require_script` body is opaque to that scan, so its author declares what it
reads. `cache_inputs` lists literal repository-relative paths. A declared file
contributes its supported cache identity, including content,
modification time, and mode; a declared directory contributes its bounded
recursive content, modification-time, and mode identity, so a gate that
inspects a surface can name that surface directly. Platform file identity and
change-time metadata also bind the persistent-worker generation fast path.
Globs, template variables, escaping paths, and duplicates are refused at
compile time, because resolving a pattern would put a search on the Stop path
instead of a fixed set of reads. A declared script plan is reused only while
every declared path keeps its exact content and supported metadata identity. Oversized
files, directory-scan overflow, escaping or nested
symlinks, and special files make that identity untrusted and bypass report
reuse instead of accepting an approximation. A contained leaf symlink is
bound to the followed target it names. `cache_inputs` is an explicit
determinism assertion over those supported identities: scripts that consult
time, randomness, network state, mutable ambient environment, undeclared
repository state, access time, ownership, ACLs, extended attributes, or the
symlink object itself must omit it. A
`require_script` that declares nothing keeps its plan off the warm path
entirely, so no report is ever reused for a script whose inputs are unknown.

Dirty regular files up to 64 MiB contribute exact SHA-256 content identity. A
larger dirty file receives only a bounded size/mtime diagnostic identity, makes
stop-policy report caching ineligible, and marks the completion worktree
untrusted; Reconc therefore never reuses a report or certifies a candidate
whose changed bytes were not hashed exactly. Normal-mode untracked directory
sentinels are recursively content-hashed under a 100,000-entry and 64 MiB
aggregate-content bound. Directory or file identity replacement during the
scan fails closed. Unsupported file metadata, dirty submodules, scan overflow,
malformed Git/TASK state, and interrupted report publication bypass generation
reuse. Alternate Git ref backends fall back to `git rev-parse`; the normal
path avoids that extra process. Reconc's own `.reconc/cache/`,
`.reconc/run/`, `.reconc/locks/`, `.reconc/reports/`, and
`.reconc/audit.jsonl` runtime artefacts are excluded from the dirty fingerprint
so report writes cannot invalidate their own cache.
`RECONC_STOP_FINGERPRINT_UNTRACKED=all` asks Git to enumerate every untracked
path; the default `normal` mode still binds all nested content below each
directory sentinel, while `no` excludes untracked paths. Matching `require_script` rules
that call the same `run-workflow-audit` runner are batched through
`--batch-json` in one process and then split back into per-rule pass/block
reports, so subprocess startup drops without weakening rule attribution. All
runtimes still keep git pre-commit as the repository backstop.
Candidates are grouped by their immutable runner and timeout key before scope
and template-context preparation; singleton or ineligible candidates pay only
the normal per-rule path, while genuine batches retain rule order and per-rule
attribution.
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
survive as orphans after a blocked hook. The evaluator admits only a consistent
`pass` outcome with exit code 0. Exit code 2 remains an attributed policy block;
timeouts, launch or process failures, every other exit, and contradictory or
unknown statuses fail closed. Timeout diagnostics use the effective configured
duration and matched path without trusting partial script output. A failed or
malformed batched workflow audit falls back to per-rule execution under the same
contract instead of being counted as handled. Workflow-audit launchers bind their
cache keys to a recursive content digest of the runner source, module files,
and generated inputs; missing or unreadable inputs fail closed. They build
cached binaries behind an atomic mkdir build lock and publish through randomized
temporary files plus rename. The launcher requires real non-symlink cache
directories and rejects linked or non-regular cached binaries and digests.
Cache-state and lock reads are bounded regular-file operations with before/after
identity checks; an unsafe cache input blocks before the audit function can
open it. Parallel agent hooks therefore wait for one rebuild instead of
stampeding the Go compiler or exposing a partially written cache binary. Direct audit Git
commands have a 15-second deadline; generated-reference build and execution
have a two-minute deadline, and every command has a two-second process/pipe
wait bound after cancellation.
TASK-claim assertion applies the same source-digest and embedded-provenance
contract through an already-open repo-local binary handle. It executes a
private verified snapshot and revalidates the canonical and snapshot identities
after process setup and before each additional claim; stale or replaced claim
authority fails before the next claim is asserted.
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
executable-TASK benchmark on Apple M1 measured 14,264,267 ns/op, 67,268 B/op,
and 571 allocs/op over five iterations on 2026-08-05. In the same run, duplicate
session mutation measured 256,433 ns/op, 16,496 B/op, and 176 allocs/op.
Incremental audit append measured 34,346,403 ns/op with no retained entries and
33,281,819 ns/op with 200 retained entries over three iterations, so normal
append allocation count no longer scales with retained-chain length. These
samples exclude process startup and Git and are observations rather than
performance promises. Reproducible Stop, audit, and concurrent-cache
benchmarks live beside their regression tests and run with
`go test ./internal/runtime/agentsession -run '^$' -bench 'RepositoryRunStopHotpath|StopPolicy' -benchmem`
and `go test ./internal/runtime/agentsession -run '^$' -bench 'VerifiedEvidencePrefix|PackedRefLookup|RecordWriteEventLargeState' -benchmem`
and `go test ./internal/audit -run '^$' -bench AuditAppendRetainedChain -benchmem`
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

`make publication-audit` deterministically scans every Git-tracked file present
in the working tree, including release notes, tests, assets, and the complete
`harness/template/repo-root-scaffold` output. It rejects private project-name
digests, personal absolute paths, agent session/share URLs and trailers,
token/key-shaped credentials, embedded URL credentials, sensitive tracked
filenames, transcript exports, placeholder `.gitkeep` residue, unreadable or
oversized files, and non-canonical tracked paths. Forbidden private names are
stored only as one-way digests, so the scanner does not reintroduce them into
the public tree. An intended worktree deletion is excluded from the current-tree
scan, while the post-boundary history scan still examines every previously
published blob. Tests construct every negative fixture from split strings and
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

- root-module and `harness/template` race tests on Ubuntu 24.04 and normal tests
  on macOS 15; whole-module root/template coverage measurement, publication
  audit, formatting, tidy, vet, pinned Govulncheck v1.7.0, and pinned
  Staticcheck v0.8.1 run once on Linux
- native Windows 2025 runtime preflight plus native binary version/help smoke
  and native PowerShell installer success, malformed
  manifest, missing asset, checksum, execution, locked/unwritable target,
  attestation, cleanup, and existing-install preservation paths;
  a focused four-minute native runtime preflight runs immediately after module
  download. The all-package root and `harness/template` suite and its Node/Bun
  adapter runtime run only after default-branch updates or an explicit manual
  `full_windows: true` dispatch, with two package test binaries at a time. The
  exact-tag Release workflow always reruns that complete native suite before
  publication; shell hook wrappers and shell policy scripts use the documented
  `sh` runtime.
- push and pull-request checks exercise the Windows installer entirely against
  the candidate binary and local fixtures, so an unpublished release candidate
  never depends on a nonexistent remote asset. After publication, a manual CI
  dispatch with `live_release: true` additionally verifies the tagged Windows
  binary and checksum manifest over HTTPS;
- SHA-pinned GitHub-owned `actions/setup-node` provisions Node.js 24.18.0 with
  implicit package-manager caching disabled. Each executable-test job packs
  exact `bun@1.3.14`, compares the tarball's npm SRI against the committed
  SHA-512 value, installs only that verified tarball, and checks the runtime
  version before executing OpenCode, Kilo Code, OMP, and Pi adapter contracts
- every CI job that executes Go provisions the SHA-pinned `actions/setup-go`
  action from `go.mod`, including the isolated release-trust job
- an isolated Ubuntu job provisions SHA-pinned `actions/setup-python`, exact
  Python `3.13.14`, and the hash-pinned external LangChain dependency lock, then
  builds the Go gateway and Go fixture and runs the local-stdio interoperability
  proof with runtime network access denied; its `LangChain MCP interoperability`
  result is mandatory on protected `main`
- clean-repository self-hosting golden path on Ubuntu and macOS across all three
  bootstrap profiles, git pre-commit, and all thirteen agent runtimes
- current-tree and post-boundary-history publication audit once in candidate CI
  and once in the tagged artifact-build path
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
separately for GitHub Actions, the root Go module, `harness/template`, and the
external Python proof lock under `scripts/tests`.
Routine version-update pull requests remain disabled on all four surfaces,
and the repository does not enable auto-merge.

The public source repository protects its default branch with the active
`Protect main` ruleset. It blocks branch deletion and non-fast-forward updates,
and requires successful Ubuntu, macOS, native Windows, LangChain MCP,
release-trust, and Go CodeQL checks for the exact candidate commit before
`main` can advance. A pull request is not mandatory, but an unchecked direct
push is rejected; maintainer fast-forwards must first obtain the same checks on
a candidate branch.
Effective rules are read back with
`gh api repos/Christopher-Schulze/reconc/rulesets/18998289`.
Repository Actions settings allow only GitHub-owned actions and require full
commit-SHA pins. Private vulnerability reporting is enabled. The active release
tag ruleset protects `reconc-v*` tags from update and deletion.

Release:

- Create the protected `reconc-vX.Y.Z` tag, then explicitly start the Release
  workflow with that tag as both workflow ref and `tag` input:
  `gh workflow run reconc-release.yml --ref reconc-vX.Y.Z -f tag=reconc-vX.Y.Z`.
  The workflow rejects branch refs so provenance binds the release tag; tag
  pushes never trigger a release.
- A published tag is never treated as a successful no-op. Replacing one
  requires the same tag-bound dispatch plus
  `-f replace_published=true`; requesting replacement when no release exists
  also fails. Existing drafts remain resumable without expanding authority.
- The tag version must be stable semantic versioning, match the source version, and have committed release notes.
- Release workflow first runs root and portable-template tests, a binary smoke
  test, and the installer gate natively on Windows 2025 against the exact tag.
  In parallel, an isolated Ubuntu prerequisite checks out the same tag and runs
  the hash-pinned official LangChain consumer against the Go gateway and Go
  fixture. Both exact-tag prerequisite jobs must pass before artifact
  publication can start. The artifact job then provisions the same pinned
  GitHub-owned Node.js runtime and exact verified Bun runtime and runs formatting,
  tidy, vet, pinned
  Govulncheck, pinned Staticcheck, race, publication, trust, and clean-repository
  self-hosting checks before building.
- `make release VERSION=<tag-version>` builds the exact flat release inventory.
- `release-manifest.json` binds the exact repository, tag, version, prerelease
  class, asset names, sizes, SHA-256 digests, and format version consumed by
  offline update discovery.
- The flat inventory includes `install.sh` and `install.ps1`; both are
  checksummed, byte-compared with tagged source, and covered by the same
  provenance manifest as the binaries.
- Release output includes deterministic SPDX 2.3 and CycloneDX 1.6 SBOMs for
  both Go modules, selected dependencies, the Go toolchain, version, and commit.
- Release output includes the Reconc license and deterministic exact license and
  notice texts for the cross-target binary dependency graph and Go toolchain.
- Every artifact is verified against `SHA256SUMS` before upload.
- Embedded deterministic build provenance binds every binary to its target and production-source digest; GitHub build-provenance attestations bind every manifest-listed artifact to the tagged workflow run.
- GitHub publication is rerun-safe and stays draft while it removes the prior
  remote asset inventory, uploads every flat local `dist/` artifact, and
  compares each remote name, byte size, and SHA-256 digest with the local
  inventory. Missing, extra, stale, or mismatched assets fail before publish;
  the final published state and inventory are read back once more.
- Draft asset reconciliation resolves the immutable GitHub release ID from the
  bounded release listing because GitHub's tag-only release endpoint excludes
  draft releases.
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
- `.reconc/repository-sync-transaction.json`
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
- `**/.reconc/repository-sync-transaction.json`
- `**/.reconc/bootstrap-*.json`
- `**/*.reconc-candidate-*`
- `**/*.reconc-remove-candidate-*`

## Security

Security posture:

- Agent payloads are untrusted input.
- Action inspection is local and deterministic: selected content is bounded,
  matched raw values are never placed in evidence, output-schema references
  cannot fetch remote data, and the implementation makes no network or model
  call. Dropping Go references is prompt rather than guaranteed memory erasure.
- LangChain and other MCP clients are enforced only for tool calls launched
  through `reconc mcp gateway`. Reconc does not inspect arbitrary external
  client code or configuration; native tools and direct downstream entries are
  reported as unenforced, never inferred safe.
- Approval authority is accepted only from a private operator-owned registry
  outside the repository. Repository policy selects bounded disclosure only;
  it cannot choose authority keys. A signed receipt is exact-call, expiring,
  single-use evidence and is not an independent boundary when the signer or
  private key is under the agent's authority.
- The action ledger has a separate strict schema whose types cannot carry raw
  arguments, results, headers, credentials, environment values, stderr,
  prompts, or arbitrary metadata. Every reader verifies the retained chain,
  archive continuity, and detached head before returning data; deletion by the
  filesystem owner remains outside the local tamper-evidence claim.
- Hook runtime payloads are size and depth bounded.
- Paths use operating-system filesystem identity and are constrained to the
  discovered repository root, including Windows junction and 8.3 aliases.
- Repository path evidence preserves legal leading and trailing spaces from
  host payload through persisted session state and evaluator matching.
- Template-aware runtime triggers compile their masked glob and capture regex
  once per immutable plan; each evaluation only applies captures and performs
  the existing bound-semantics check.
- Payload command strings are matched as data and are not executed. Matching
  compares the literal words a shell would execute, so quote removal and
  backslash escapes cannot hide a forbidden program, and an undecodable
  construct fails closed instead of comparing unresolved text. Deny matching
  folds the program name's case; evidence matching does not.
- The runtime builds one ordered command-evidence index per evaluation, storing
  normalized command/result semantics together with raw syntax, outcome, and
  freshness epoch. Normalized expected command invocations are prepared once
  and bounded observed-command parses are reused for repeated forbidden,
  required, composite, and assurance checks; incomplete or dynamic syntax
  remains fail-closed.
- Repeated evidence-file checks share a bounded cache for the current
  evaluation. It revalidates identity, mode, size, and modification time on
  every hit, reuses only stable bounded bytes, and fails closed on replacement
  or metadata drift; missing-file results are likewise invalidated if the path
  appears later in the same evaluation.
- After a stable snapshot is available, a separate bounded logical-match memo
  keys substituted file bindings, content digest, file identity, and all
  substring/line-count/optional flags through an exact immutable
  length-delimited key rather than per-assertion hashing. It preserves ordered negative reasons
  for top-level and composite checks. The same evaluation memoizes template
  match contexts with cloned capture maps, including deterministic errors, so
  repeated rules do not rebuild equivalent derived evidence. That memo has a
  4 MiB retained-byte budget in addition to its entry cap; oversized entries
  bypass storage and FIFO eviction accounts for keys, strings, captures,
  errors, slice slots, and the simultaneously retained defensive clone.
- Batch path normalization and policy evidence resolution use one
  evaluation-local `pathidentity` prospective resolver. Shared existing
  ancestors are `Lstat`-revalidated before reuse; missing suffixes remain
  uncached, preserving symlink/reparse, containment, case, and replacement
  semantics while eliminating repeated root resolution and reducing ancestor
  walks.
- Write-epoch normalization runs through the same resolver in one ordered pass
  and merges normalized aliases with the maximum observed epoch, preserving
  causal freshness when absolute and relative spellings collide.
- Stop report reuse binds exactly the repository paths the compiled policy
  names, including the script target and the files a `require_script` declares
  through `cache_inputs`. A script that declares nothing is never reused.
- Only policy-authored `require_script` entries execute subprocesses. The
  declared path is repo-relative, the script itself must be a real executable
  file rather than a symlink, and the directory it resolves through must stay
  inside the repository, so neither a lexical `..` nor an intermediate
  directory symlink moves execution outside the repository root. The declared
  timeout and SIGTERM-to-SIGKILL grace are both clamped before they become
  durations, so no policy value can wrap the conversion or outlive the cap.
  Top-level rules may declare `kill_timeout_sec`; composite script sub-checks
  deliberately use the same bounded five-second default because their stable
  schema exposes only `timeout_sec`.
  These scripts are trusted repository code, not sandboxed input. The filtered
  environment reduces incidental secret exposure but retains `HOME` for common
  user-level toolchain caches and configuration, which therefore remain
  visible to the script.
- Audit log is opt-in via `RECONC_AUDIT=1`.
- Non-portable current lockfile root markers are a hard stale/fail condition;
  equivalent clones and worktrees share the portable `.` identity.
- Current lockfiles carry a self-digest over the canonical payload, bind the
  complete source identities with `source_digest`, and decode into a strict
  typed runtime plan. Runtime path patterns are validated during that plan
  build and retained as bounded immutable matcher programs; patterns outside
  the compiled-memory admission budget retain a validated doublestar fallback.
  Migrated legacy locks additionally prove embedded-rule and MCP parity against
  reparsed current sources.
- Runtime-plan loading coordinates only callers for the same repository root.
  Lockfile reads, source walks, freshness hashing, decoding, and compilation for
  different roots proceed independently. Before publishing a newly compiled
  plan, the evaluator revalidates both the lockfile hash and complete source
  freshness so an out-of-lock mutation cannot publish stale state.

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

## v0.9.6 To v0.9.7 Migration

Source-owned installations build source version `0.9.7`. The latest published
binary remains `reconc-v0.9.6` until the protected `reconc-v0.9.7` tag is
created and the manual release workflow completes; no installation command
should use the new tag before that publication exists.

Format 6 keeps its representation and migration chain unchanged, but its
rule-kind field matrix is now part of the immutable
`schemas/v6/policy-lock.schema.json` identity at `reconc-v0.9.7`. Existing
format-6 lockfiles that still name the v0.9.6 schema are accepted through an
input-only compatibility alias; a refresh writes the v0.9.7 identity. Formats
1 through 5 keep their exact previously published schema URLs and migration
behavior. No mutable `main` URL, tag rewrite, or format-version bump is used.
The current v6 schema also owns the exact composite-check surface instead of
inheriting the broader historical v4 shape. Sub-checks never carry their own
`before_paths` or `when_paths`; the parent composite rule owns activation, and
all legacy formats still reject those dead fields during typed runtime-plan
construction after migration.

Native assurance now fails explicitly when a configured package manager is
missing or mismatched, validates complete applicability lists before matching,
and rejects invalid compiled command policies during plan loading. Substantive
proof authoring distinguishes the omitted 24-hour default from explicit
`max_age_hours: 0`, which disables staleness without disabling timestamp or
future-skew validation. Package-script and dependency-pin gates share the same
single-leading-UTF-8-BOM package JSON contract.

The local publication contract authorizes the new current tag while it is an
unreleased candidate and verifies the new local v6 bytes, digest, `$id`, and
registry mapping. The online HTTP publication verifier belongs to the
tag-bound release workflow and must run only after the exact protected tag is
published.

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

The current source line is `v0.9.x`; the source version is `v0.9.7`. The latest
published release remains the immutable `reconc-v0.9.6` tag. The
`reconc-v0.9.7` format-6 schema identity is an unreleased candidate until its
protected tag exists and the release workflow has published matching assets.
Release artifacts are produced only through an explicit manual Release
workflow dispatch for an existing `reconc-vX.Y.Z` tag; tag pushes never
publish a release.
