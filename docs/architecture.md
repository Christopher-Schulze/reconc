# reconc -- Architecture

Short tour of how the pieces fit together, aimed at contributors and
anyone building on top of the library. For user-facing command
reference see `commands.md`.

## Pipeline

Every `reconc` invocation moves data through some subset of this pipe:

```
       repo root
  (rules + MCP mappings)
           │
           ▼
      ┌────────┐
      │ ingest │        discovery + source loading
      └───┬────┘
          ▼
      ┌────────┐
      │ parser │        validate rules, resolve templates, expand scopes
      └───┬────┘
          ▼
      ┌──────────┐
      │ compiler │      canonical JSON + SHA-256 digest + lockfile write
      └───┬──────┘
          ▼
  .reconc/policy.lock.json
          │
          ▼
      ┌─────────┐
      │ runtime │       evaluate inputs against the lockfile
      └────┬────┘
           ▼
       CheckReport + FixPlan
           │
           ▼
     completiongate
           │
           ▼
     CompletionReport
           │
           ▼
       proofbundle
           │
           ▼
      ProofBundle
```

The proof-bundle exporter is a privacy boundary, not a command transcript.
Every command receipt exposes only a bounded executable summary. Its
`command_hash` is a deterministic SHA-256 grouping key over the sanitized
executable identity, never over the normalized command or its arguments, so
public bundles do not provide an offline argument-guessing oracle. Paths,
home/user identity, known token forms, and assignment values are redacted with
boundary-aware replacement, bounded UTF-8 output, and a maintained adversarial
test corpus; regex recognition is defense in depth rather than secret
discovery.

`refresh` stops at the lockfile. `check` / `ci` / `assert` / `can`
load the lockfile and run the runtime evaluator. `fix` / `explain`
also use the runtime then render the result. `done` binds the evaluated
candidate through `completiongate`; `proof` renders that same candidate through
`proofbundle`. `why`, `diff`, and `sources` inspect compiled policy state.
Host-native MCP events and configured generic MCP identities enter through the
same compiled lockfile. Exact selectors classify a call as repository read,
repository write, command, or external before session evidence is considered;
unclassified or malformed calls never become positive repository evidence.
`impact` takes a separate non-publishing compiler branch: it adds one candidate
source in memory, builds the same typed runtime plan, and compares it with the
fresh current plan over a strict privacy-bounded corpus. The branch never
writes the policy lock or runtime state and refuses any `require_script` rule
before evaluation so arbitrary repository code cannot violate the
side-effect-free replay contract.
`policy author` extends that non-publishing branch with embedded schema
validation and a selected real `policies/*.yml` provenance path. Preview stays
read-only; explicit adoption re-prepares under the bootstrap repository lock,
publishes only the selected fragment, requires the production compiler to emit
the exact previewed lock bytes, validates the fresh runtime, and restores its
target and lock snapshots on failure.

## Package map

```
buildprovenance/ deterministic target/source identity + byte-only binary inspection
harness/         embedded immutable advanced harness pack
schemas/         published policy schemas plus digest-checked Go embedding for offline authoring
internal/
  action/         pure action contract, strict values, immutable matchers, evaluator, traces, and exact cache
  actionapproval/ canonical signed approval contracts, authority registry, provider boundary, and MCP mapping
  actioninspect/  strict MCP results, offline output schemas, deterministic content inspection, and safe withholding
  actionevidence/ strict versioned control maps and deterministic privacy-bounded local evidence reports
  actionledger/   privacy-bounded typed events, private retained chain, lifecycle queries, and verification
  actionledgerexport/ verified synthetic minimized Impact Lab export with explicit omissions
  actionstate/    trusted identities, budgets, approval consumption, reservations, and crash-safe local state
  adopt/          convention detector, rule suggestions, and stack-pack recommendations
  agentguide/     embedded agent-integration guide + section lookup
  assurance/      bounded native layout/source/manifest/proof gates + per-run fact graph
  atomicfile/     identity-bound write-on-change, private-parent, and atomic publication primitives
  audit/          private SHA-256-linked JSONL decision evidence + detached head + bounded rotation
  boundedexec/    concurrency-safe bounded stdout/stderr capture for subprocess boundaries
  boundedio/      exact-size reads for untrusted and repository-controlled files
  bootstrap/      init, repository sync/remove/recovery, portable receipts, journals, and binary resolution
  cireport/       bounded provider-neutral SARIF 2.1.0, JUnit XML, and GitHub report rendering
  cli/            command dispatch plus responsibility-owned command modules
  commandmeta/    canonical dependency-neutral command, flag, help, and output contract
  commandproof/   staged candidate-bound command-success receipts
  compiler/       lockfile builder: digest, writer, conflicts, migrations, lock
  completion/     bash / zsh / fish completion generators
  completiongate/ exact final-completion report over policy, candidate, evidence, and TASK state
  contextsize/    token-budget guard for canonical entrypoints + active TASK
  customruntime/  non-executable third-party runtime manifests, neutral transport, and conformance
  errors/         typed exception hierarchy (PolicySourceError, LockfileError, ...)
  execfile/       cross-platform regular-file and executable validation
  extractor/      prose-to-rule heuristic scanner (regex-only, no LLM)
  gitexec/        shared hermetic Git subprocess environment and argv boundary
  grokacp/        strict Grok ACP stdio client + cross-platform leader IPC stop steering/probing
  harnesspack/    strict versioned harness-pack manifest, archive, digest, and compatibility contract
  hooks/          typed registry + generators + install/uninstall + activation + scaffold sync
  ingest/         discovery + source loading (AGENTS.md, .reconc.yml, presets, globals)
  impactlab/      strict format-2 repository/action corpora, exact action-delta review, and deterministic policy comparison
  lockdiff/       structural lockfile comparison with envelope, source-inventory, and provenance reporting
  filelock/       cross-platform process locks
  jsonl/          bounded decode, validation, locked publication, recovery, and indexed tail reads
  manpage/        groff reconc(1) generation from the canonical command table
  mcpgateway/     tools-only MCP stdio enforcement, SDK boundary, child ownership, and orchestration
  parser/         YAML-to-Rule validation + template expansion + scope expansion
  pathidentity/   Unix symlink + Windows reparse/8.3 filesystem identity
  privatefs/      shared private-directory, lock, owner/mode, hard-link, and descriptor boundary
  policy/         Rule / Scope / Source / Kind / Mode types
  policyauthor/   schema-backed preview, effective explanation, and transactional adoption
  policyproof/    tamper-evident unresolved policy-decision receipts
  presets/        bundled policy packs (embed.FS) + user overlays
  proofbundle/    deterministic portable JSON and Markdown completion evidence
  repositoryignore/ canonical target-repository runtime-ignore contract
  runtime/        staged evaluation, normalization, matching, violations, remediation, Git, and subprocesses
  retention/      bounded runtime storage lifecycle + owned temp cleanup
  runtime/agentsession/  hook payload handlers, session evidence state,
                  fingerprinted Stop snapshots/cache, Git and completion state,
                  run-state store (the package the
                  hook-runtime threat model below describes)
  safename/       strict lower-kebab identifiers for user-selected assets
  schema/         canonical public JSON contract URLs + enterprise override
  shellcommand/   bounded shell parsing and executable-command discovery
  stackdetect/    shared bounded manifest/source stack discovery
  tasklifecycle/  typed TASK profiles + recoverable state transactions
  templates/      bundled rule-shape templates (embed.FS) + user overlays
  tui/            dependency-free terminal dashboard
  usercli/        atomic running-build install + exact bare-command PATH verification
  yamlbound/      shared bounded YAML mapping admission before alias expansion
```

`cmd/reconc/main.go` parses argv, delegates to
`cli.Run`, and translates the returned error into an exit code.
Within `internal/cli`, `cli.go` owns only public errors, top-level
dispatch, and canonical usage. Compile, evaluate, inspect, explain, CI,
bootstrap, canonical init, source analysis, quality, maintenance, catalog, metadata,
hook, workflow/session, TASK lifecycle, repository-run, and deep-doctor logic
live in responsibility-owned files without a second router or compatibility
wrapper.
Hook generation
is separate from merge/install logic, runtime lockfile trust is separate from
rule evaluation, and Stop decisions are separate from general session event
handling.

Large package owners follow those same responsibility seams internally. The
runtime evaluator separates orchestration, script execution, normalization,
rule evaluation, matching, and violation construction. Agent-session Stop
handling separates cache policy and storage from fingerprints, Git state, and
completion snapshots. JSONL separates append/publication, validation,
journaling/recovery, locking, and tail reads. Hook CLI handling separates
command routing, lifecycle operations, runtime dispatch, response adaptation,
timing, and claim operations. These are source-ownership boundaries only; they
do not add package layers or duplicate public APIs.

Runtime evaluations with an owned lifecycle use the context-bearing evaluator
entry points. That lifecycle is the parent of each `require_script` timeout, so
MCP request cancellation and gateway shutdown terminate the script before the
policy deadline when necessary. Unix termination covers the complete process
group and preserves the configured `SIGTERM`-to-`SIGKILL` grace; Windows keeps
the native immediate process-kill backend. The synchronous CLI, completion
gate, and agent-hook entry points expose no independent request lifecycle;
their compatibility entry points intentionally use a background parent while
the compiled `timeout_sec` and `kill_timeout_sec` bounds remain mandatory.

<!-- BEGIN RECONC GENERATED HOOK REFERENCE -->
## Canonical hook verification matrix

Generated from the hook registry. Capability describes registry evidence, not observed host liveness.

| Host | Surface | Expected runtime events | Capability | Operator exercise |
|---|---|---|---|---|
| `antigravity` | `cli` | antigravity-pre-invocation, antigravity-pre-tool-use, antigravity-post-tool-use, antigravity-stop, antigravity-post-invocation | documented | Start antigravity in the disposable repository and exercise its documented positive, negative, compaction, and Stop routes. |
| `claude-code` | `cli` | claude-session-start, claude-user-prompt-submit, claude-pre-tool-use, claude-permission-request, claude-permission-denied, claude-post-tool-use, claude-post-tool-use-failure, claude-mcp-before, claude-mcp-after, claude-stop, claude-stop-failure, claude-session-end, claude-notification, claude-subagent-start, claude-subagent-stop, claude-pre-compaction, claude-post-compaction, claude-compaction-recovery | documented | Start claude-code in the disposable repository and exercise its documented positive, negative, compaction, and Stop routes. |
| `codex` | `cli` | codex-session-start, codex-user-prompt-submit, codex-pre-tool-use, codex-permission-request, codex-post-tool-use, codex-mcp-before, codex-mcp-after, codex-stop, codex-session-end, codex-subagent-start, codex-subagent-stop, codex-pre-compaction, codex-post-compaction | documented | Start codex in the disposable repository and exercise its documented positive, negative, compaction, and Stop routes. |
| `cursor` | `cursor-cli-interactive` | cursor-session-start, cursor-user-prompt-submit, cursor-pre-tool-use, cursor-post-tool-use, cursor-stop, cursor-session-end, cursor-workspace-open | documented | Start cursor in the disposable repository and exercise its documented positive, negative, compaction, and Stop routes. |
| `cursor` | `cursor-cli-print` | cursor-session-start, cursor-user-prompt-submit, cursor-pre-tool-use, cursor-post-tool-use, cursor-stop, cursor-session-end, cursor-workspace-open | documented | Start cursor in the disposable repository and exercise its documented positive, negative, compaction, and Stop routes. |
| `cursor` | `cursor-cloud` | cursor-user-prompt-submit, cursor-pre-tool-use, cursor-before-shell-execution, cursor-post-tool-use, cursor-after-file-edit, cursor-post-tool-use-failure, cursor-after-shell-execution, cursor-subagent-start, cursor-subagent-stop, cursor-pre-compaction, cursor-stop | documented | Start an approved Cursor cloud-agent run for the disposable repository and exercise the documented cloud routes. |
| `cursor` | `cursor-desktop-agent` | cursor-session-start, cursor-user-prompt-submit, cursor-pre-tool-use, cursor-before-shell-execution, cursor-post-tool-use, cursor-after-file-edit, cursor-post-tool-use-failure, cursor-after-shell-execution, cursor-before-mcp-execution, cursor-after-mcp-execution, cursor-subagent-start, cursor-subagent-stop, cursor-pre-compaction, cursor-stop, cursor-session-end, cursor-workspace-open | documented | Open the disposable repository in Cursor Agent and exercise the documented positive, negative, MCP, subagent, compaction, and Stop routes. |
| `cursor` | `cursor-desktop-cmd-k` | cursor-session-start, cursor-user-prompt-submit, cursor-pre-tool-use, cursor-before-shell-execution, cursor-post-tool-use, cursor-after-file-edit, cursor-post-tool-use-failure, cursor-after-shell-execution, cursor-before-mcp-execution, cursor-after-mcp-execution, cursor-subagent-start, cursor-subagent-stop, cursor-pre-compaction, cursor-stop, cursor-session-end, cursor-workspace-open | documented | Open the disposable repository in Cursor Cmd+K and exercise only the documented Cmd+K routes. |
| `cursor` | `cursor-tab` | cursor-after-tab-file-edit | documented | Open the disposable repository in Cursor and accept one Tab edit. |
| `devin-cli` | `cli` | devin-session-start, devin-user-prompt-submit, devin-pre-tool-use, devin-permission-request, devin-post-tool-use, devin-stop, devin-session-end, devin-post-compaction | documented | Start devin-cli in the disposable repository and exercise its documented positive, negative, compaction, and Stop routes. |
| `git-pre-commit` | `pre-commit` | - | documented | Stage a disposable denied change and attempt a commit without bypassing hooks. |
| `github-copilot` | `cli` | copilot-session-start, copilot-user-prompt-submit, copilot-pre-tool-use, copilot-permission-request, copilot-post-tool-use, copilot-post-tool-use-failure, copilot-stop, copilot-session-end, copilot-notification, copilot-subagent-start, copilot-subagent-stop, copilot-pre-compaction | documented | Start github-copilot in the disposable repository and exercise its documented positive, negative, compaction, and Stop routes. |
| `grok` | `tui` | grok-session-start, grok-user-prompt-submit, grok-pre-tool-use, grok-permission-denied, grok-post-tool-use, grok-post-tool-use-failure, grok-stop, grok-stop-failure, grok-session-end, grok-notification, grok-subagent-start, grok-subagent-stop, grok-pre-compaction, grok-post-compaction | documented | Start grok in the disposable repository and exercise its documented positive, negative, compaction, and Stop routes. |
| `kilo` | `cli` | kilo-session-start, kilo-user-prompt-submit, kilo-pre-tool-use, kilo-permission-request, kilo-post-tool-use, kilo-post-tool-use-failure, kilo-stop, kilo-continuation-accepted, kilo-continuation-failed, kilo-continuation-unavailable, kilo-continuation-suppressed, kilo-session-end, kilo-pre-compaction, kilo-post-compaction | includes inferred routes | Start kilo in the disposable repository and exercise its documented positive, negative, compaction, and Stop routes. |
| `kilo` | `vscode` | kilo-session-start, kilo-user-prompt-submit, kilo-pre-tool-use, kilo-permission-request, kilo-post-tool-use, kilo-post-tool-use-failure, kilo-stop, kilo-continuation-accepted, kilo-continuation-failed, kilo-continuation-unavailable, kilo-continuation-suppressed, kilo-session-end, kilo-pre-compaction, kilo-post-compaction | includes inferred routes | Open the disposable repository in Kilo Code's VS Code host and exercise the documented project-plugin routes. |
| `kimi-code` | `cli` | kimi-session-start, kimi-user-prompt-submit, kimi-pre-tool-use, kimi-permission-request, kimi-permission-result, kimi-post-tool-use, kimi-post-tool-use-failure, kimi-stop, kimi-stop-failure, kimi-interrupt, kimi-session-end, kimi-subagent-start, kimi-subagent-stop, kimi-pre-compaction, kimi-post-compaction, kimi-notification | documented | Start kimi-code in the disposable repository and exercise its documented positive, negative, compaction, and Stop routes. |
| `omp` | `cli` | omp-session-start, omp-user-prompt-submit, omp-pre-tool-use, omp-user-bash, omp-user-python, omp-permission-request, omp-permission-result, omp-post-tool-use, omp-post-tool-use-failure, omp-stop, omp-session-end, omp-pre-compaction, omp-post-compaction | documented | Start omp in the disposable repository and exercise its documented positive, negative, compaction, and Stop routes. |
| `opencode` | `cli` | opencode-session-start, opencode-user-prompt-submit, opencode-pre-tool-use, opencode-permission-request, opencode-post-tool-use, opencode-post-tool-use-failure, opencode-stop, opencode-continuation-accepted, opencode-continuation-failed, opencode-continuation-unavailable, opencode-continuation-suppressed, opencode-session-end, opencode-pre-compaction, opencode-post-compaction | includes inferred routes | Start opencode in the disposable repository and exercise its documented positive, negative, compaction, and Stop routes. |
| `pi` | `cli` | pi-session-start, pi-user-prompt-submit, pi-pre-tool-use, pi-user-bash, pi-post-tool-use, pi-post-tool-use-failure, pi-stop, pi-continuation-requested, pi-continuation-failed, pi-continuation-suppressed, pi-session-end, pi-pre-compaction, pi-post-compaction | includes inferred routes | Start pi in the disposable repository and exercise its documented positive, negative, compaction, and Stop routes. |
| `zcode` | `cli` | zcode-session-start, zcode-user-prompt-submit, zcode-pre-tool-use, zcode-permission-request, zcode-post-tool-use, zcode-post-tool-use-failure, zcode-stop | documented | Start ZCode in the disposable repository and exercise SessionStart, UserPromptSubmit, PreToolUse, PermissionRequest, PostToolUse, PostToolUseFailure, and Stop. |

<!-- END RECONC GENERATED HOOK REFERENCE -->

<!-- BEGIN RECONC GENERATED SCHEMA REFERENCE -->
## Canonical schema contracts

Generated from `internal/schema`. Canonical URLs are immutable publication identities; aliases remain input-only.

| Artifact | Schema | Formats | State | Canonical URL | Local source |
|---|---|---|---|---|---|
| `action-control-map` | `v1` | 1 | `current` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v1/action-control-map.schema.json> | `schemas/v1/action-control-map.schema.json` |
| `action-control-map-authorities` | `v1` | 1 | `current` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v1/action-control-map-authorities.schema.json> | `schemas/v1/action-control-map-authorities.schema.json` |
| `action-control-map-signature` | `v1` | 1 | `current` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v1/action-control-map-signature.schema.json> | `schemas/v1/action-control-map-signature.schema.json` |
| `action-evidence` | `v1` | 1 | `current` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v1/action-evidence.schema.json> | `schemas/v1/action-evidence.schema.json` |
| `action-ledger` | `v1` | 1 | `legacy` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v1/action-ledger.schema.json> | `schemas/v1/action-ledger.schema.json` |
| `action-ledger` | `v2` | 1 | `current` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v2/action-ledger.schema.json> | `schemas/v2/action-ledger.schema.json` |
| `completion-report` | `v1` | 1 | `current` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v1/completion-report.schema.json> | `schemas/v1/completion-report.schema.json` |
| `custom-runtime-conformance` | `v1` | reconc-custom-runtime-conformance/v1 | `current` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v1/custom-runtime-conformance.schema.json> | `schemas/v1/custom-runtime-conformance.schema.json` |
| `custom-runtime-liveness` | `v1` | reconc-custom-runtime-liveness/v1 | `current` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v1/custom-runtime-liveness.schema.json> | `schemas/v1/custom-runtime-liveness.schema.json` |
| `custom-runtime-manifest` | `v1` | reconc-custom-runtime/v1 | `legacy` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v1/custom-runtime-manifest.schema.json> | `schemas/v1/custom-runtime-manifest.schema.json` |
| `custom-runtime-manifest` | `v2` | reconc-custom-runtime/v2 | `current` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v2/custom-runtime-manifest.schema.json> | `schemas/v2/custom-runtime-manifest.schema.json` |
| `global-diagnostic` | `v1` | reconc.global-diagnostic/v1 | `current` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v1/global-diagnostic.schema.json> | `schemas/v1/global-diagnostic.schema.json` |
| `global-lifecycle` | `v1` | reconc.global-lifecycle/v1 | `current` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v1/global-lifecycle.schema.json> | `schemas/v1/global-lifecycle.schema.json` |
| `harness-pack-manifest` | `v1` | reconc.harness-pack/v1 | `current` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v1/harness-pack-manifest.schema.json> | `schemas/v1/harness-pack-manifest.schema.json` |
| `installation-receipt` | `v1` | 1 | `current` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v1/installation-receipt.schema.json> | `schemas/v1/installation-receipt.schema.json` |
| `neutral-hook-request` | `v1` | reconc-neutral-hook-request/v1 | `current` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v1/neutral-hook-request.schema.json> | `schemas/v1/neutral-hook-request.schema.json` |
| `neutral-hook-response` | `v1` | reconc-neutral-hook-response/v1 | `current` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v1/neutral-hook-response.schema.json> | `schemas/v1/neutral-hook-response.schema.json` |
| `policy-config` | `v1` | - | `legacy` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v1/policy-config.schema.json> | `schemas/v1/policy-config.schema.json` |
| `policy-config` | `v2` | - | `legacy` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v2/policy-config.schema.json> | `schemas/v2/policy-config.schema.json` |
| `policy-config` | `v3` | - | `legacy` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v3/policy-config.schema.json> | `schemas/v3/policy-config.schema.json` |
| `policy-config` | `v4` | - | `current` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v4/policy-config.schema.json> | `schemas/v4/policy-config.schema.json` |
| `policy-fix-plan` | `v1` | 1 | `current` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v1/policy-fix-plan.schema.json> | `schemas/v1/policy-fix-plan.schema.json` |
| `policy-lock` | `v1` | 1 | `legacy` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v1/policy-lock.schema.json> | `schemas/v1/policy-lock.schema.json` |
| `policy-lock` | `v2` | 2 | `legacy` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v2/policy-lock.schema.json> | `schemas/v2/policy-lock.schema.json` |
| `policy-lock` | `v3` | 3 | `legacy` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v3/policy-lock.schema.json> | `schemas/v3/policy-lock.schema.json` |
| `policy-lock` | `v4` | 4 | `legacy` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.4/schemas/v4/policy-lock.schema.json> | `schemas/v4/policy-lock.schema.json` |
| `policy-lock` | `v5` | 5 | `legacy` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v5/policy-lock.schema.json> | `schemas/v5/policy-lock.schema.json` |
| `policy-lock` | `v6` | 6 | `current` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.7/schemas/v6/policy-lock.schema.json> | `schemas/v6/policy-lock.schema.json` |
| `policy-report` | `v1` | 1 | `current` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v1/policy-report.schema.json> | `schemas/v1/policy-report.schema.json` |
| `proof-bundle` | `v1` | 1 | `current` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v1/proof-bundle.schema.json> | `schemas/v1/proof-bundle.schema.json` |
| `release-manifest` | `v1` | reconc.release/v1 | `current` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v1/release-manifest.schema.json> | `schemas/v1/release-manifest.schema.json` |
| `repository-install` | `v1` | 1 | `current` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v1/repository-install.schema.json> | `schemas/v1/repository-install.schema.json` |
| `repository-sync-plan` | `v1` | reconc.repository-sync-plan/v1 | `legacy` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v1/repository-sync-plan.schema.json> | `schemas/v1/repository-sync-plan.schema.json` |
| `repository-sync-plan` | `v2` | reconc.repository-sync-plan/v1 | `current` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v2/repository-sync-plan.schema.json> | `schemas/v2/repository-sync-plan.schema.json` |
| `repository-sync-report` | `v1` | reconc.repository-sync-report/v1, reconc.repository-sync-verify/v1 | `legacy` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v1/repository-sync-report.schema.json> | `schemas/v1/repository-sync-report.schema.json` |
| `repository-sync-report` | `v2` | reconc.repository-sync-recovery/v1, reconc.repository-sync-report/v1, reconc.repository-sync-resolution/v1, reconc.repository-sync-verify/v1 | `current` | <https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v2/repository-sync-report.schema.json> | `schemas/v2/repository-sync-report.schema.json` |

<!-- END RECONC GENERATED SCHEMA REFERENCE -->

## Key invariants

1. **Byte-stable private portable lockfile.** Two compiles of identical sources
   in different clones or worktrees produce identical bytes. Format 6 uses `.`
   as its repository/discovery root marker and stores only logical source
   paths plus SHA-256 content digests, never raw source bodies or physical
   global-policy paths. Compiler emits canonical JSON (sorted keys, indent-2,
   trailing newline). Source digest is SHA-256 over the same canonical form.
   This enables rsync-style drift detection and git-friendly diffs without
   publishing instruction or global-policy content.

2. **Fail-closed on tampering.** Unknown document or rule field, unknown rule kind, malformed YAML,
   stale lockfile, non-portable current root marker, unsupported schema URL --
   every degradation path raises a typed error rather than silently
   treating the situation as "pass".

The policy source order is canonical and complete: the eight rule-bearing
tiers are serialized in stable precedence order, and custom runtime manifests
have the next explicit rank even though their manifest bodies are not rule
documents. Duplicate rule IDs are always validation errors, including across
tiers; no source silently overrides another. Diagnostics name the rule ID and
both source locations, while source ranking remains available for deterministic
candidate insertion and digest ordering.

3. **Owned publication.** Write paths publish atomically or through an explicit
   transaction. Canonical lockfile bytes are compared before publication, so an
   unchanged compile performs no filesystem write. Bootstrap install is
   create-only, emits candidate files for drift, and rolls back only
   transaction-owned unchanged files. Repository sync requires an exact saved
   plan digest, takes a hermetic read-only Git snapshot, re-plans under one
   transaction lock, mutates only exact portable-receipt-owned bytes, and
   verifies receipt, embedded pack, binary, generated-policy, and hook
   identities. Before target mutation it publishes a bounded, strict,
   self-digested journal with exact before and after identities. Explicit
   recovery finalizes a verified complete after-image or rolls back exact
   transaction images; any external edit is preserved and refused. Directory
   identity is not guessed after a crash, so empty created parents may remain.
   TASK lifecycle transactions independently bind every touched regular file
   and moved source to exact bytes and mode, revalidate the full precondition
   set and each operation, and publish moves through an atomic no-clobber
   hard-link transition whose intermediate state is recoverable. Prepared and
   committed journal phases plus private random markers bind newly created
   parent directories to safe rollback or commit finalization; unproven or
   non-empty directories are never removed.
   Bootstrap removal treats portable ownership as its maximum authority,
   SHA-verifies owned files, strips only managed blocks, and preserves drift
   and user-owned paths.
   Bootstrap-created artifact verification and rollback retain opened file and
   parent identities through chmod, hashing, durability, and rooted cleanup;
   external target replacement is reported and preserved. Hook merges and
   uninstalls preserve
   unrelated host configuration. ZCode's repository-local nested JSON merger
   owns only exact Reconc process entries and the required `hooks.enabled`
   activation. Kimi Code's user-global TOML is a separate
   explicit lifecycle: one process-locked marker block is merged, replaced, or
   removed atomically, while bootstrap and scaffold transactions remain
   repository-only. Bounded JSONL writers rotate under a process
   lock before append. Write, sync, close, unlock, and CLI output failures are
   propagated instead of being reported as successful publication.

4. **Explicit side effects.** Policy refresh, bootstrap, hook installation, TASK
   mutation, retention, and hook event handling own their documented files.
   Read-only commands never refresh policy. `RECONC_AUDIT=1` is still required
   for the optional decision audit log. Policy impact analysis compiles only in
   memory, publishes only an explicitly requested report or corpus output, and
   refuses external script rules before they can execute.

5. **One stable interactive command.** `install-cli` atomically publishes the
   exact running executable through bounded file streams and verifies the
   binary resolved by bare `reconc`. Private, synced, identity-checked file
   backups provide exact byte-and-mode rollback without full-binary heap
   retention. Under the same global lock it publishes a
   strict, private, checksum-bound ownership receipt only after PATH identity
   passes. Mutating bootstrap performs that install and identity check before
   repository writes; transactional verification checks it again.

6. **Advisory compile lock.** `.reconc/.compile.lock` is a reusable OS-backed
   exclusive file lock. A second compiler fails immediately, process exit
   releases ownership automatically, and no timestamp-based stale reaping can
   steal a live lock. Refresh discovers only the prospective repository root
   before locking; it captures the complete authoritative policy-source bundle
   after acquisition and refuses publication if discovery resolves to a
   different root. The repository and `.reconc` directories stay bound through
   opened `os.Root` identities, and unsafe or replaced lock objects fail before
   locking an unrelated inode.

7. **Satisfiable conflict analysis.** Static command contradictions follow
   runtime `require_command` semantics: any configured alternative can satisfy
   the rule. A forbid/require pair is reported only when their exact trigger
   scopes overlap and one forbid rule blocks every required alternative.

8. **Exact MCP identity and effect.** MCP classification uses the complete
   `(platform, server_fingerprint, tool)` key. Fingerprint presence never
   falls back to a weaker selector. Effect-specific RFC 6901 fields must resolve
   to typed repository-contained paths or exact commands before policy or
   evidence handling. Unknown identity, malformed input, and unknown outcome
   are non-evidentiary.

## Go-Only Action Plane (Draft)

RECONC-0008 remains Draft. v0.9.7 implements strict action
authoring, deterministic legacy MCP lowering, one canonical format-6 action
plan, immutable typed matcher programs, a derived MCP compatibility view, and
`reconc why action`. The transport-neutral pure evaluator now implements strict
request normalization, exact predicates and precedence, provenance monotonicity,
phase isolation, fail-closed errors, bounded redacted traces, and exact
resampled in-memory decision caching. `reconc impact` invokes the production
compiler and evaluator for strict offline `action_pre` and `action_post`
scenarios, exact current expectations, candidate deltas, approval-state
assertions with separate snapshot and transition coverage, and reviewed
newly-allowed or newly-blocked gates. `internal/actioninspect` now strictly
decodes bounded MCP results, validates local Draft 2020-12 output schemas with
offline internal references and a bounded RE2-compatible pattern subset,
scans policy-selected argument, result, and progress fields through a
content-digested detector pack, classifies unsupported content and annotations,
and creates payload-free evidence or bounded withheld results. The separate
compiled-plan boundary exposes detector policies to inspection only through
read-only scalar, indexed-field, and membership operations; plan-owned slices
and pointer tokens never cross the boundary or require request-time deep
cloning. Present/null field identities bind canonical value bytes once, while
missing states bind the explicit pointer state. The evidence control-map
validator uses one immutable canonical fact registry and direct indexed
membership rather than per-control selector maps. Report construction retains
an independent final identity validation because compact identity JSON and the
indented publication representation are different contracts. The separate
Action Ledger records typed payload-free lifecycle evidence through private
atomic append, bounded rotation, retained-chain verification, and detached-head
recovery. `reconc action log tail|stats|verify|export` provides deterministic
non-creating queries and verified minimized Impact Lab export. Existing durable
append transactions are resolved before their snapshot is returned; missing
state stays absent. `internal/actionevidence` now derives exact facts from the
current policy and lock, retained ledger window, read-only action state,
reverified signed approval receipts, and Impact Lab scenarios. Versioned
built-in maps reference SOC 2, GDPR, the HIPAA Security Rule, and the EU AI Act;
strict digest-pinned or Ed25519-signed custom maps cannot set status or override
facts. JSON and Markdown reports remain technical evidence mappings only, not
organizational assessment, legal determination, or external assurance. `reconc
action evidence export|verify` exposes that read-only evidence path.
`reconc mcp gateway` invokes the enforcement primitives around every routed
live tool call.

The evaluator's immutable value boundary provides allocation-free count and
indexed traversal plus exact canonical encoded size. Compiled predicates own
prepared path-base state and one-time scalar sort keys. At the gateway boundary,
one `PreparedEvaluation` owns the normalized request and exact cache binding for
lookup, evaluation, and store; no filesystem or mutable observation crosses
that request-scoped object.

Ledger-selected fields are valid only for `arguments` during `pre_call` and
`result` during `post_result`; progress and observation events carry none.
Their pointer and value identities bind the policy declaration index, policy,
lock, tool contract, source, canonical value state, repository identity, and
the active identity-key generation. Missing keyed identity produces explicit
incomplete evidence, never an unkeyed fallback. Rotation refuses to prune the
first retained event of any active call. Verification reports whether event and
call completeness were actually evaluated separately from whether they were
complete, so an invalid chain cannot manufacture a completeness claim.

`internal/actionapproval` and `internal/actionstate` now own the implemented
approval boundary in addition to trusted identity and cumulative budgets.
Canonical approval requests bind one exact call, policy and lock, executable,
server and tool contract, trusted principal and context, credential labels,
selected keyed argument identities, taint, repository effect, rule trace,
budget reservation, issuance, expiry, and nonce. Canonical Ed25519 receipts
carry exact approve or reject decisions. Verification accepts only an active
operator-configured authority key and consumes an approval at most once under
the same atomic cross-process state transaction as its budget reservation.
Cancellation, malformed input, rejection, expiry, replay, unavailable
authority, persistence failure, and crash-orphan reconciliation are explicit
fail-closed transitions with payload-free evidence.

The authority registry is a bounded private regular file outside the
repository. Repository policy may select only safe argument summaries for an
informed decision; it cannot select authority keys, private key material, or
the authority process. A prompt or signer under the same agent's authority is
not an independent approval boundary. MCP `2026-07-28` input-required and MCP
`2025-11-25` standard form elicitation are exact transport mappings for request
state and a signed receipt; clients without the required capability or valid
response receive an explicit approval-required failure. The gateway resamples
and consumes these boundaries before dispatch and again before result delivery.

The private versioned state store serializes processes, journals atomic state
replacement, preserves capacity across governing-generation changes, blocks
clock rollback and unsafe key rotation, and models reserved, approval-pending,
dispatched, indeterminate, and terminal transitions. Generic project-root
retention protects this durable state instead of silently returning capacity.

The implemented topology is one local, tool-only stdio MCP gateway around one
operator-selected downstream stdio MCP server. Every routed `tools/call`
enters one canonical compiled action plan before dispatch, and every downstream
result or progress event enters the same plan before upstream delivery.
Calls do not share a gateway-wide state mutex. Immutable policy and inspection
work proceeds concurrently, while the durable action-state and ledger owners
linearize only their own transitions. Each call owns a bounded ordered progress
queue; the transport reader admits notifications without waiting for inspection
or upstream output, and result publication waits for that queue to drain or
cancel before recording the terminal transition.
Native LangChain tools, clients configured directly against the downstream
server, prompts, resources, sampling, roots, tasks, HTTP, SSE, and arbitrary
framework calls remain outside that boundary.

The dependency direction is intentionally one-way:

~~~text
policy/parser -> compiler -> immutable action plan -> pure action evaluator
                                                       |
operator state -> budgets/approvals/actioninspect/actionledger -> MCP stdio gateway
                                                       |
                                               external MCP client
~~~

The pure evaluator owns decisions, predicates, provenance, traces, and exact
cache eligibility without filesystem, process, network, clock, CLI, or MCP SDK
dependencies. IO packages inject typed snapshots. The gateway owns protocol and
child-process lifecycle but cannot reimplement policy semantics. Existing `mcp`
authoring becomes compatibility input lowered into the same action plan; the
custom-runtime bridge remains a lifecycle normalizer rather than a second
action engine.

The product remains one Go binary. LangChain integration uses LangChain's own
MCP adapter to launch that binary over stdio. Reconc ships no Python or
TypeScript LangChain adapter. The disposable consumer job installs a hash-pinned
external Python environment, invokes converted tools directly, and denies
runtime socket connections; it is test infrastructure, not product code or a
release artifact. The built Go fixture owns downstream observations and
cancellation markers, while a separate raw Go client independently proves both
supported protocol generations. The complete trust model,
authority modes, resource limits, failure matrix, approval and budget state
machines, privacy-bounded ledger, conformance vectors, and package ownership are
in [RECONC-0008](rfcs/RECONC-0008-go-only-action-plane.md).

The interoperability matrix is Reconc `0.9.7`, Go MCP SDK `v1.7.0`, current
protocol `2026-07-28`, legacy protocol `2025-11-25`, official
`langchain-mcp-adapters==0.3.2`, `langchain-core==1.5.4`, MCP Python SDK
`1.29.0`, Python CI `3.13.14`, and Go fixture format `1`. The external adapter
currently negotiates the legacy protocol only and completes approval through
an externally signed standard form-elicitation response. Fresh Python client
sessions do
not own Reconc principal, credential, run, session, budget, approval-replay,
policy, or ledger identity; those remain operator-bound and durable below the
selected Reconc home. An explicit stateful Python session is tested separately.

External framework configuration is not an observable authority surface.
`status` and deep `doctor` therefore expose the invariant
`explicit_routes_only / not_inspected / unenforced` instead of attempting to
parse arbitrary Python or claiming a direct downstream entry is safe. Native
LangChain tools and alternate MCP entries remain outside the gateway graph.

## Key external contracts

- **Lockfile schema** (`$schema` in policy.lock.json): every published URL is
  immutable; any represented-shape change receives a new schema version.
  Migration chain in `compiler/migrations.go` walks older versions forward.

- **CheckReport / CompletionReport / FixPlan schemas**: the same immutable-URL
  rule applies. Additive and breaking shape changes both receive a new schema
  version; breaking semantic changes also require a superseding RFC.

- **Published schema documents**: `internal/schema` owns all 36 Draft 2020-12
  contracts as independently versioned registry entries. Each entry binds one
  local path, immutable release-tagged `$id`, release asset, SHA-256 digest,
  enterprise mirror path, current or legacy state, and input-only compatibility
  aliases. Current policy authoring uses v4, custom-runtime manifests and
  repository sync use v2, and current lockfiles use v6; v1-v5 lock schemas and
  every other superseded artifact version remain
  validated inputs. Release output derives the complete schema inventory from
  the registry, byte-compares it locally, and verifies every canonical URL
  online after publication. Runtime validation remains offline.

- **Declarative custom-runtime contract**: `.reconc/runtimes/*.json` is the
  only repository source. Manifests are bounded non-symlink regular files,
  strictly decoded, filename-bound to a reserved-safe `custom:<name>` identity,
  and digested with policy sources. They select exact RFC 6901 pointers only;
  Reconc evaluates no manifest code, expressions, templates, shell text, or
  network references. `hook bridge` resolves one compiled manifest and routes
  its neutral payload through the existing agent-session handlers. Missing
  host guarantees produce `unsupported` rather than synthetic enforcement.
  `hook conform` executes only bounded JSON fixtures and requires request,
  response, timeout, failure, liveness, and privacy proofs.

- **Action authoring and lock contract**: strict `actions` authoring owns
  canonical tools, selectors, effects, phases, conditions, decisions, failure
  policy, cache policy, budgets, approvals, detectors, ledger policy, defaults,
  and provenance. Legacy `mcp` authoring remains compatibility input and lowers
  into the same plan. Format 6 contains
  `actions` and forbids a parallel `mcp` runtime representation; legacy v1-v5
  lock schemas remain immutable migration inputs.

- **Exit codes 0/1/2**: stable across all subcommands for agent
  consumption. 0 = pass or warn, 1 = runtime/input error, 2 = at
  least one blocking violation.

- **Public runtime env vars** (`NO_COLOR`, `RECONC_HOME`, `RECONC_AUDIT`,
  `RECONC_AUDIT_VERBOSE`, `RECONC_CLAUDE_STATE_DIR`,
  `RECONC_SCHEMA_BASE_URL`, `RECONC_STOP_FINGERPRINT_UNTRACKED`, and
  `RECONC_GROK_STEER`, plus the host-owned `KIMI_CODE_HOME`): stable names.
  Adding a new one is additive; renaming or removing needs a major version
  bump. Debug and installer variables are catalogued separately in
  `docs/commands.md`.

## v0.9 Product Lifecycle

[RECONC-0007](rfcs/RECONC-0007-cli-product-lifecycle.md) is the frozen
implementation contract for the v0.9 CLI product layer. The global installation
receipt, `doctor --global`, update, uninstall, canonical init, versioned harness
packs, portable repository ownership, digest-bound sync
planning/resolution/apply/recovery/verification, in-memory generated-policy
rebuild, registered policy migration, and ownership-safe removal are
implemented through the same product and bootstrap boundaries.

The target adds four ownership layers without creating a second bootstrap
engine:

```
global manager -> installation receipt -> installed CLI
                                         |
                                         v
                                  bootstrap engine
                                         |
                    embedded pack -> repository receipt
                                         |
                                         v
                             digest-bound repo sync
```

- `internal/usercli` remains the global binary identity owner. It owns the
  locked receipt, manager classification, global diagnostic, update, and
  uninstall boundaries. Receipt reads execute once and revalidate their
  observed generation if a writer lock appears concurrently.
- `internal/bootstrap` remains the only repository transaction owner.
  Canonical `init` and repository sync compose its plan, candidate, receipt,
  verification, journal, recovery, rollback, and path-identity primitives.
  Adoption joins the same canonical lock for its read-merge-write publication,
  while bootstrap and sync share one real-directory or strict worktree-file
  `.git` identity contract.
- Sync planning sanitizes ambient Git routing and gives `git write-tree` an
  ephemeral object database with the real object database as read-only
  alternate. It therefore binds HEAD and index without writing repository Git
  objects, command-proof state, or any other persistent Reconc state.
- Same-platform receipt-owned binaries advance from the exact running
  executable. Cross-platform binaries require an explicitly checksum-pinned
  `use-binary` resolution; blockers never inherit implied overwrite authority.
- Versioned public harness packs are immutable embedded inputs to bootstrap,
  never mutable Git checkouts or arbitrary copied directories.
- Global update and repository sync have separate locks, plans, reports, and
  verification. Package managers retain update and removal authority over
  their files.
- The portable `.reconc/install.lock.json` records repository ownership without
  physical checkout paths. Private transaction receipts may bind a checkout
  for rollback but cannot expand portable ownership. Private bootstrap history
  is bounded to the current pair plus two validated historical pairs; malformed,
  foreign, partial, and linked state is preserved rather than treated as
  cleanup authority.

The update trust chain is release identity -> checksum -> embedded build
provenance -> mandatory GitHub build-provenance attestation -> global receipt
-> embedded pack digest -> repository receipt -> exact sync plan. Offline
updates require both the selected asset's Sigstore bundle and the trusted root.
Native installer policy is a separate boundary and may make attestation
optional or required explicitly. Any broken required link is an actionable
refusal, never an inferred owner or partial success.

Release licensing has one copied project-license owner and one generated
third-party-notice owner. The notice generator unions the exact dependency
graphs of all five static binary targets, accepts only bounded root regular-file
licenses/notices, includes the Go toolchain license, and renders deterministic
component identities plus source-text digests. Release verification regenerates
and byte-compares the notice before checksums and provenance are accepted.

## Request flow example: `reconc check --write src/x.go`

1. `cli.Run(argv, version, stdout, stderr)` dispatches to `runCheck`.
2. `runCheck` builds `runtime.ExecutionInputs` from flags, captures
   `start := time.Now()`.
3. `runtime.CheckRepoPolicy(repo, inputs)`:
   - `ingest.NewSourceLoadContext(repo)` walks up for `.reconc/`,
     `.reconc.yml`, `AGENTS.md`, etc.
     The context binds the canonical root identity, config identity, and
     per-default-glob fragment inventory for the complete source transaction;
     `LoadPolicySourcesWithContext` revalidates that snapshot before and after
     source reads. The compiler passes this context through its lock-protected
     load, so default fragments are not discovered twice. Policy globs use
     bounded segment enumeration with explicit pattern, directory, match, and
     source-count caps; `**` has no recursive special meaning.
     Inline fenced-policy extraction scans each bounded context source once,
     tracks line numbers incrementally, and stops at its per-source block cap
     before retaining another block body.
     Repository source reads use `boundedio.ReadFileSnapshot`, retaining the
     opened regular-file identity and comparing it with the post-read path and
     root identities before bytes enter the source bundle.
     A compiler config is decoded into one bounded YAML mapping; include and
     preset extraction consume that same representation, so source-loading
     fields cannot drift through independent parses. The parser then enforces
     the canonical typed-graph limits before rule construction: 4,096 rules,
     256 checks or list items per rule, 1 KiB pattern strings, 16 KiB command
     strings, 64 KiB message strings, 32 YAML levels, 131,072 YAML nodes,
     262,144 alias-expanded nodes, 1,024 aliases, and 4 MiB of decoded scalar
     bytes. Alias expansion, duplicate keys, and trailing YAML documents fail
     closed before the typed graph is retained.
     The frozen source bundle is converted to one provenance record set and
     content digest; the same records feed both source-digest computation and
     lock-payload serialization.
     Canonical JSON boundaries expose normalized bytes together with the
     decoded `UseNumber` tree, so action validation does not marshal the same
     normalized payload a second time.
     Budget validation reuses the exact canonical argument-byte length once per
     evaluation whenever a selected budget declares `argument_bytes`; other
     budget dimensions do not trigger serialization.
   - `internal/runtime/lockfile.go` performs a 16 MiB bounded read and validates
     schema, version, repository root, migration state, and source freshness.
     Current format-6 locks prove freshness from the complete lock digest plus
     one bounded source-bundle digest pass. Migrated legacy locks additionally
     reparse sources and prove exact embedded rule and canonical-action parity.
     Its strict recursive decoder is the single JSON-token boundary: duplicate
     keys, Unicode, depth, root shape, numeric values, and trailing data are
     checked while the `UseNumber` tree is retained. Rules/actions are encoded
     once for typed decoding, and the already compiled action plan is reused by
     the runtime plan builder.
   - The validated payload is decoded once into an immutable typed runtime plan.
     ID, kind, pre-command composite, and scope metadata are indexed before any
     evidence is evaluated; malformed or unknown typed fields fail closed. The
     plan also validates every runtime path pattern once and retains bounded,
     immutable matcher programs for repeated scope and evidence comparisons;
     action evaluators likewise recompile and own one detached immutable plan at
     construction. Every pointer, condition, matcher, index, and cardinality
     boundary is admitted once; evaluations do not recursively rescan the
     compiled plan or revalidate each pointer program.
    template-capture patterns additionally retain their masked glob, capture
    regex, and bound-substitution state; the lockfile wire format is
    unchanged. Each evaluation builds one ordered command-evidence index with
    normalized command/result semantics, outcome, and freshness epoch, prepares
    normalized expected shell invocations once, and caches observed invocation
    extraction by command text for the evaluation lifetime. Raw command syntax
    remains available to forbid and assurance reporting paths. Evidence-file
     checks use a bounded evaluation-local snapshot cache keyed by the resolved
     path; every hit revalidates file identity, mode, size, and modification
     time before reusing metadata or bounded content. Evidence checks then reuse
     a second bounded memo keyed by that stable file identity/content digest,
     substituted file binding, and an exact immutable encoding of every assertion option; negative matches and
     ordered reasons are cached without changing violation order. Template
     match-context construction is likewise memoized per evaluation with cloned
     immutable capture maps under both a 4 MiB retained-byte budget and a 4,096-entry cap.
     Literal templates return no capture map. High-cardinality path
    normalization uses an evaluation-local prospective resolver that
    revalidates shared existing ancestors before reusing their filesystem
    identities; missing suffixes are never trusted from string state alone.
    Write-epoch keys use the same resolver and one pass over the write-path
    sequence, merging aliases by maximum epoch after normalized containment.
     Action predicate evaluation reuses one immutable materialized context
    root per evaluation; context-member pointers retain binary-search lookup,
    while arguments, results, and progress are already canonical roots. The
     production compiled-plan path therefore avoids rebuilding the same object
     and revalidating its pointer program for every predicate.
     Runtime-plan cache misses use repository-root-scoped singleflight rather
     than holding the cache mutex across filesystem I/O. Lock and source
     identities are revalidated immediately before a compiled plan is
     published; unrelated roots never serialize behind that work. Cache-hit
     source freshness derives content, metadata, identity, and aggregate-byte
     accounting from one opened stable file snapshot. Replacement before that
     open may yield one coherent new observation; replacement after it fails
     closed, and rejected observations never consume the aggregate budget.
     Runtime evidence/report collections use a local membership map plus an
     ordered slice, preserving first-seen output while avoiding quadratic
     duplicate scans at bounded high cardinalities.
     Harness-pack build and archive load validate canonical inventories before
     payload retention, enforce per-file and remaining aggregate byte budgets
     on every read, and compare actual decompressed bytes with the manifest.
     Context-size accounting bounds requested paths before filesystem work,
     reports token and byte totals in 64-bit JSON fields with saturating sums,
     and inspects only stable contained regular-file identities.
     Prose extraction deduplicates by semantic rule content and emits readable
     bounded IDs with stable digest suffixes whenever lossy slugging could
     collide; canonical bare repository filenames use an explicit allowlist.
   - Normalises the input paths against the repo root.
   - For each rule in the lockfile: applies the scope filter
     (`ruleScopeMatches`), then dispatches to the per-kind
     evaluator (`evalDenyWrite`, `evalRequireRead`, ...).
   - Collects violations, calls `report.Finalize()` which derives
     decision / counts / actions / rule_ids.
4. `maybeAudit("check", report, version, start)` appends one chained JSONL
   entry iff
   `RECONC_AUDIT=1`.
5. Output is rendered as terse, JSON, text, SARIF 2.1.0, or JUnit XML depending
   on flags. CI-native formats are built from one provider-neutral finding
   model, bounded before serialization, and atomically published when
   `--output` is set.
6. Returns `&CLIError{ExitCode: 2}` if the decision is block;
   otherwise nil.

## Adding a new subcommand

1. Write `runFoo(args []string, stdout, stderr io.Writer) error` in the
   responsibility-owned `internal/cli/*_cmd.go` file, or create one when the
   command introduces a distinct responsibility. Use `CLIError{ExitCode,
   Message}` for typed failures.
2. Add a `case "foo": return runFoo(argv[1:], ...)` to the dispatcher
   switch.
3. Add one complete entry to `internal/commandmeta/catalog.go`; root help,
   shell completion, and the man page consume that canonical contract.
4. Write tests in `internal/cli/cli_test.go`: happy path + at least
   one error path + `--help`.
5. Document in `docs/commands.md` under the right category.

The typical commit diff for a new subcommand touches the dispatcher, one
responsibility-owned command file, canonical command metadata, focused tests, and
`commands.md`.

## Adding a new rule kind

1. `internal/policy/types.go`: add the `Kind` constant + mark it
   valid in `Kind.Valid()`.
2. `internal/parser/parser.go`:
   - add any required-field validation for the new kind
   - if it needs new fields on `policy.Rule`, add them with JSON +
     YAML tags + `omitempty`
3. `internal/compiler/compiler.go`: if `ruleToMap` needs to emit
   new fields, extend it (preserve byte-stability by only emitting
   when set).
4. `internal/runtime/evaluator.go`: write `evalFooKind`, wire into
   the dispatcher in `evaluateRule`.
5. `internal/runtime/remediation.go`: add a case in
   `buildStepsForKind` so the fix plan has helpful steps.
6. Extend `internal/compiler/conflicts.go` if the new kind has
   meaningful pair-wise conflicts with existing kinds.
7. Tests at every layer.

Rule-kind field ownership is centralized in `internal/parser/unknown_fields.go`.
The matrix is applied after template defaults are merged, before typed rule
construction, and again at the runtime lock boundary through the exported
parser allowlist. This prevents a valid union field from becoming an ignored
field on another kind. Composite inline checks have their own kind matrix and
retain the rule ID, check index, kind, and source path in diagnostics. The
current v6 lock schema carries the same kind conditions as an overlay; legacy
published schemas remain immutable.

The parser owns one compile-scoped template cache and validates every cached
resolution again before returning the parsed policy. Preset manifests and
policy sources share `yamlbound` node, alias, depth, scalar, and
single-document admission. Compiler conflict analysis groups rules by
length-delimited normalized semantic keys and caps materialized pairs with an
explicit truncation record; it never repeats list sorting inside a pair loop.

Template grammar is owned by `internal/templates/variables.go`: `{name}` is
the only capture token, balanced comma alternatives remain glob syntax, and
unescaped malformed braces fail closed. Parser path masking, compiler
warnings, runtime capture matching, and substitutions all consume this scanner;
literal braces require escaping on both sides.

## Dependency graph

```
  commandmeta ──┬──► cli
                ├──► completion
                └──► manpage

  cli ──┬──► compiler ──► parser ──► ingest
        │       ▲
        │       └── migrations, conflicts, lock
        │
        ├──► runtime ──┬──► policy
        │              ├──► assurance ──► policy
        │              └── template substitution, script runner, git
        ├──► cireport
        │
        ├──► hooks
        ├──► bootstrap ──► harnesspack, stackdetect, presets, hooks, repositoryignore
        ├──► adopt ──► stackdetect, presets
        ├──► extractor
        ├──► lockdiff
        ├──► audit
        ├──► actionledger ──► action, actionstate, jsonl
        ├──► actionledgerexport ──► actionledger, impactlab
        ├──► actionevidence ──► action, actionledger, actionstate, impactlab
        ├──► mcpgateway ──► action, actionapproval, actioninspect, actionledger, actionstate, MCP Go SDK
        ├──► contextsize
        ├──► commandproof
        ├──► completiongate ──► commandproof, policyproof, runtime, tasklifecycle
        ├──► proofbundle ──► completiongate, commandproof, policyproof, schema
        ├──► retention
        ├──► tasklifecycle
        ├──► usercli ──► atomicfile, pathidentity
        ├──► agentguide (embed)
        ├──► templates  (embed)
        ├──► presets    (embed)
        └──► completion

  harness (embed) ──► harnesspack
```

`commandmeta` imports no product package, so CLI, completion, and manpage share
the public surface without a cycle. Nothing below `cli` imports `cli`. The compiler does not know about the runtime;
the serialized lockfile is the boundary. `schema` is the single owner of public
contract URLs. Runtime lockfile loading imports compiler only for registered
migrations, current-envelope validation, and source-digest freshness
validation. The runtime then owns the strict typed plan and its deterministic
indexes; evaluators no longer extract fields from generic rule maps. Formats 1
through 5 migrate in memory to the current body-free portable envelope; freshly
compiled lockfiles never persist a checkout root or source body.

## Threat model: hook runtime

`reconc hook runtime <event>` accepts a JSON payload on stdin from
the registered agent platforms. That payload is **untrusted
input** even when the agent is cooperative: an agent may be buggy
and produce malformed JSON, a malicious agent build may try to inject
adversarial payloads, and payload schemas drift as the platforms
evolve. The runtime handlers need a documented policy for every
class of hostile input.

### Hard limits (enforced by the stdin reader)

| Limit | Value | Rationale |
|---|---|---|
| Max payload bytes | **64 MiB** (67 108 864) | Large file-edit and generated-document bodies remain usable while the bounded reader still rejects unbounded JSON input. |
| stdin read timeout | **5 seconds** | Prevents agent hangs from wedging the hook call. Typical payloads arrive < 50 ms. |
| Max JSON nesting depth | **32 levels** | Prevents stack-busting via deeply nested payloads. |
| Max persisted live session state | **1 MiB** | Bounds full-file state publication and recovery cost. |
| Evidence collections | **item + byte caps per field; 64 chained segments** | A full live collection rotates losslessly; non-segmentable evidence or chain failure creates durable project taint. |
| Audit record | **32 KiB** | Bounds one locked JSONL append. |
| Audit/run storage | **2 MiB live + 2 archives each** | Fixed rings and transition-only run records prevent repository-local log growth. |
| Action ledger | **64 KiB per record / 4 MiB live + 2 archives** | Bounds payload-free lifecycle evidence; rotation refuses to prune an active call whose retained beginning would be lost. |
| MCP gateway frames / arguments / results | **10 MiB / 8 MiB / 10 MiB** | Strict framed JSON rejects overflow, duplicate keys, malformed values, and unsupported protocol shapes before dispatch or delivery. |
| MCP gateway tools / pages / metadata | **512 / 64 / 8 MiB aggregate** | Bounds discovery, validates each contract, requires tool `_meta` to be absent or empty, and rejects unsafe schemas, icons, extension metadata, collisions, and churn. |
| MCP gateway tool icons | **32 / 48 KiB each / 2,048 px per side / 4,194,304 pixels** | Accepts only fully decoded self-contained PNG or JPEG data URIs; remote URLs, animated or incompletely decoded formats, MIME contradictions, and decompression bombs fail closed. |
| MCP gateway calls / approvals / progress | **4 / 4 / 128 events and 1 MiB aggregate per call** | Bounds parallel lifecycle state, approval retention, and inspected progress before forwarding. |
| MCP gateway child stderr | **256 KiB retained** | Drains without blocking, counts overflow, classifies retained text, and emits only a redacted summary. |
| MCP gateway operator diagnostic | **4 KiB per serialized line** | Removes invalid UTF-8 and control characters; diagnostics contain typed reasons or redacted summaries, never raw child output. |
| Hook output | **8 KiB per route** | Prevents verbose host output from consuming agent context. |
| Bun adapter process output | **8 KiB combined stdout + stderr** | OpenCode, Kilo, OMP, and Pi concurrently drain both pipes; overflow, invalid UTF-8, timeout, and truncated decision JSON fail according to the registry route policy. |
| Hook worker request frame | **64 MiB payload + 64 KiB envelope** | Keeps complete supported hook payloads while bounding protocol metadata and buffering. |
| Hook worker response frame | **128 KiB at the adapter** | Bounds framed JSON and escaped 8 KiB route output before parsing. |
| OpenCode/Kilo continuation state | **1,024 sessions / 10 accepted continuations each** | Bounded generation and in-flight state suppress duplicate idle delivery without storing prompts, tool payloads, or model output. |
| OMP native Stop continuation | **8 turns / 29 s internal timeout** | The OMP host caps awaited main-agent `session_stop` continuation and its 30-second handler deadline stays outside Reconc's fail-closed timeout; an aborted host signal releases immediately. |
| Pi settled continuation | **1,024 sessions / 10 requested continuations each** | Bounded generation and in-flight state suppress duplicate `agent_settled` requests. Pi returns no delivery acknowledgement, so requested delivery is never recorded as acceptance. |
| Compaction context | **4 KiB** | Restores control-plane orientation without replaying logs or task files. |
| Native assurance file | **4 MiB** | Rejects oversized source, manifest, or proof inputs before allocation. |
| Native assurance run | **4,096 files / 32 MiB reads** | Bounds aggregate source and evidence inspection across all gates. |
| Assurance findings | **50 + omitted-count marker** | Keeps policy output useful without consuming agent context. |
| Policy source | **8 MiB each / 4,096 files / 64 MiB aggregate** | Bounds repository and fragment ingestion before compilation. |
| Parsed policy graph | **4,096 rules / 256 checks or list items / 1 KiB patterns / 16 KiB commands / 64 KiB messages** | Bounds typed rule construction before matching; YAML depth, node, alias, expanded-node, and decoded-scalar-byte ceilings reject structural amplification. |
| Custom runtime manifest | **256 KiB each / 32 manifests / 32 routes each** | Bounds declarative bridge compilation and prevents adapter configuration from becoming executable input. |
| Custom runtime host payload | **8 MiB / 32 levels / 65,536 object members / 65,536 array items / 13 selected fields / 2 MiB retained** | Go 1.27 `jsontext` validates the complete object; one pointer trie shares ancestor traversal, skips unselected subtrees, and materializes only budgeted selected values. The largest shipped fixture is 107 bytes. |
| Custom runtime conformance suite | **1 MiB / 128 cases** | Bounds offline third-party adapter verification. |
| Hook liveness | **64 runtimes / 32 routes each / 256 KiB aggregate** | Covers the built-in registry plus the bounded custom-runtime set without unbounded status state. |
| Policy lock / execution input | **16 MiB each; lock 1,048,576 JSON items; input 262,144 items / 64 levels** | Bounds evaluator control input before allocation. Current locks retain rules/actions as raw typed subtrees; events append once into a capacity-planned accumulator. |
| Policy evidence / TASK control file | **4 MiB each** | Bounds file-backed checks and executable TASK state before parsing. |
| Portable workflow-audit input | **64 MiB per file / 100,000 walked entries** | Strict regular-file and real-directory readers reject links, FIFOs, special files, replacement, and partial over-budget trees; task schemas and legacy prune policies use a narrower 1 MiB cap. |
| Auxiliary subprocess capture | **64 KiB to 64 MiB by boundary** | TASK claim diagnostics use 64 KiB per stream; lifecycle, offline-hook, promotion, and generated-reference probes use 1 MiB; workflow/SBOM/license-notice commands use 16 MiB to 32 MiB; Stop Git uses 32 MiB; publication-history Git uses 64 MiB. Overflow fails the invoking operation instead of growing process memory. |

Breaches use the registry's platform-specific blocking response or exit code for
PreToolUse, permission, and Stop. Observation and cleanup routes fail open with
bounded warnings.

Bounded evidence uses raw, immutable segments instead of truncation. Each
segment carries the repository identity, session identity, policy-lock hash,
monotonic index, previous digest, and every evidence collection from the sealed
live epoch. Consumers replay the verified digest chain before evaluation, so
rotation changes storage shape rather than policy meaning. One identity-index
set per full replay keeps string and command-result deduplication linear across
the complete sealed and live chain. A triggering event is retried only after
the previous live epoch is durably sealed. Segment-chain
tampering, an oversized individual item, storage failure, or exhaustion of the
64-segment session budget creates a project-scoped taint that survives session
cleanup and is inherited on the next SessionStart.

Taint is a third terminal state, not a policy pass. PreToolUse blocks every
repository write and command while preserving read-only diagnosis; claims, CI,
saved policy passes, and completion remain unavailable. Repository run enabled
keeps Stop blocked. Repository run disabled records an uncertified termination
and releases Stop without clearing taint. Explicit user interrupt remains the
host escape and also does not clear taint.
Resolution requires no active session, the exact SHA-256 token of the current
taint, and a bounded operator reason. The resolver writes a durable receipt
containing the abandoned taint before removing the live marker. A successor
session therefore starts a new evidence window without converting the abandoned
epoch into certified evidence.

### Fail-closed vs fail-open

Decision is per-event based on the security role of the event:

| Event | Malformed payload | Reasoning |
|---|---|---|
| `SessionStart` | **fail-open** (exit 0, stderr warn) | Orientation failure must not wedge the host session; PreToolUse and Stop remain the enforcement points. |
| `PreToolUse` | **fail-closed** (exit 2) | This event GATES a write/command; uncertain input must not allow. |
| `PostToolUse` | **fail-open** (exit 0, stderr warn) | Observation-only; blocking here doesn't prevent already-done damage and just disrupts the session. |
| `PostToolUseFailure` | **fail-open** (exit 0, stderr warn) | Same as PostToolUse. |
| `Stop` | **fail-closed** (exit 2) | GATES session completion; uncertain input must block. |
| `SessionEnd` | **fail-open** (exit 0, stderr warn) | Cleanup-only; forced close shouldn't propagate errors. |
| MCP pre-action | **host capability** | Cursor's native pre-hook can deny an exact or strict-unclassified call. OpenCode/Kilo/OMP/Pi/ZCode generic hooks enforce configured identities but cannot soundly identify unconfigured MCP calls. |
| MCP post-action | **fail-open, non-evidentiary on uncertainty** | Post-action blocking cannot undo a side effect. Positive evidence requires exact identity, valid selected values, and explicit success. |

The CLI applies the registry failure policy after handler execution as well as
during input decoding, so a handler cannot accidentally make an allow-route
blocking. The CLI resolves the repository once per request into an opaque
`ResolvedRepoRoot`; only the agent-session package can construct that handle.
Normalization, the selected normalized handler, Stop/compaction/MCP internals,
and liveness receive the same handle, eliminating repeated filesystem-identity
discovery without weakening stored-state root checks. Runtime identity is an
explicit request argument and is never communicated through mutable process
environment state. Successful dispatch records per-route liveness outside the repository.
Each runtime route has a small six-hour marker: the common path is one `stat`,
zero locks, zero JSON reads, and zero writes; a due route refresh updates the
bounded aggregate status used by `reconc hook status`.

OpenCode, Kilo Code, OMP, and Pi own one `reconc hook worker` child per live
plugin repository. The internal protocol is newline-framed JSON format 1 with
printable request IDs, explicit event and repository fields, one object payload,
strict UTF-8/JSON decoding, and sequential response order. The adapter serializes
concurrent host callbacks. Cancellation kills the worker and yields to the host;
a route timeout kills it and applies that route's timeout policy without a second
evaluation. Startup failure, protocol mismatch, or a crash before a response
uses the remaining route budget for the existing one-shot path. Protocol drift
disables reuse for that plugin instance; a post-handshake crash can restart on
the next event. Shutdown sends a bounded acknowledgement and kills any child
that does not exit; parent stdin closure also terminates the worker. A running
worker keeps its current binary until plugin shutdown, so an installed upgrade
is picked up by the next plugin instance without mutating an in-flight request.
The worker caches `ResolvedRepoRoot` plus an immutable typed policy plan. Root
resolution eagerly materializes the filesystem object identity before caching,
including Go's lazy Windows file ID; reuse proves that same object with
`os.SameFile` and resolves again after same-path replacement or alias drift.
An oversized request frame is drained to its newline without retaining its
contents and yields one protocol-error response with an empty ID when the
request identity cannot be proven. A bounded drain ceiling turns an
unterminated or excessively long frame into terminal worker loss; a completed
oversized frame leaves the stream synchronized for the next request.
Single-fragment request frames are copied once at their exact size. Fragmented
frames begin from the buffered reader's bounded window and grow in fourfold,
limit-capped steps, avoiding both a hard-limit preallocation and repeated
geometric reallocations near the 64 MiB request ceiling.
The generated JavaScript adapter accumulates response chunks in a geometrically
growing bounded byte buffer and performs one line/remainder copy per frame;
small stream chunks therefore cannot trigger quadratic prefix copying. The
buffer is reduced to the unread remainder after each response and never grows
beyond the 128 KiB response ceiling.
Every policy check still performs bounded lock-byte and source-bundle identity
reads. Changed lock bytes rebuild the plan; changed source identity invalidates
it and fails closed until `reconc refresh`. Session state, taint, and binary
selection remain fresh per request. Shell-only hosts continue using one-shot
execution and no daemon, listener, socket, or network surface is introduced.
Active-session pointer updates use an optimistic unchanged-value read. A
transient Windows sharing conflict is rechecked under the existing pointer
lock, while persistent read or validation failures remain fatal.

Custom runtimes do not enter the built-in registry and cannot override it.
Their host adapter invokes `reconc hook bridge <name> <host-event> [repo]` and
owns the outer process timeout declared in the manifest. Reconc reads one
bounded JSON object through a strict Go 1.27 `jsontext` validation pass and a
single shared-ancestor pointer-trie selection pass. Unselected values use
`SkipValue`; only selected raw spans within the retained-byte budget become
`json.Number`-preserving Go values. Reconc copies only those declared fields into the neutral
payload, validates the fresh compiled source digest, executes the existing
handler, emits one bounded neutral response, and records a collision-resistant
route liveness key. Status and deep doctor keep manifest configuration,
degraded guarantees, and observed route liveness as separate facts.

Passive lifecycle events are observation-only. They validate an existing
session under its cross-process lock but do not create missing state, rewrite
an active-session pointer, normalize/publish unchanged JSON, or manufacture
policy evidence. Duplicate pre-tool and permission delivery can reuse one
bounded external decision only with a stable tool-call ID and an exact SHA-256
identity over canonical tool input, policy-lock bytes, session-state bytes,
the current compiled policy-source digest, and project evidence-taint bytes.
The identity is re-sampled after cache read;
any concurrent mutation, missing identity, malformed entry, unsupported result,
or size-bound failure falls back to a fresh evaluation. Atomic publication
keeps concurrent writers safe, one record per session bounds storage, and
SessionEnd removes the record.

Cursor generation is registry-driven down to native event, matcher, runtime
route, response mode, failure policy, timeout, loop limit, and documented
surface. `postToolUse` is the authoritative successful generic signal;
`postToolUseFailure` is authoritative failure; `afterShellExecution` is
passive because Cursor provides no exit status there. Sessionless
`workspaceOpen` takes a dedicated liveness-only path, redacts user identity,
and never enters session evidence. Registry-derived `surface_events` prevents
the probe and public status contract from maintaining a second Cursor matrix.
Material signatures deduplicate specialized write events against generic
post-tool delivery.

Kimi Code generation is registry-driven into a user-global TOML marker block
rather than a repository artifact. Its internal entrypoint first discovers an
explicit Reconc repository from the current working directory and otherwise
no-ops, so global host configuration cannot turn arbitrary directories into
Reconc targets. The adapter validates the native route name, snake_case
envelope, repository-contained `cwd`, session identity, tool input, and error
shape. Kimi's control contract is exit code 2 on `PreToolUse`,
`UserPromptSubmit`, and `Stop`; host crashes, all other non-zero exits, and
timeouts remain fail-open and cannot be upgraded into an enforcement claim.
Kimi post-tool output has no authoritative exit status, so it is
non-evidentiary for command success.

ZCode generation is registry-driven into `.zcode/config.json`. The nested
merger preserves foreign settings, events, and commands while owning the exact
Reconc process entries under `hooks.events`; invalid `hooks` or `events`
shapes fail unless explicit force first publishes a private content-addressed
backup. The adapter validates the native snake_case envelope, repository
identity, session identity, available tool-call identity, tool input, result,
and error shapes across all seven documented events. Hard pre-tool blocks use
exit code 2, permission denials use the native decision object, and Stop uses
native block JSON; malformed fail-closed requests use exit code 2. Observation
routes and host timeouts remain fail-open, and Stop continuation uses ZCode's
three-consecutive-block host bound. Generic tool payloads can enforce configured
MCP identities but cannot soundly identify an unconfigured MCP call.

OpenCode and Kilo plugins are generated transport adapters. Shell outcomes are
normalized from the exact integer `output.metadata.exit` into the neutral Go
payload. Their session-owned worker uses the common framed protocol above; the
one-shot recovery runner concurrently drains both pipes, applies one combined
output budget, validates UTF-8, and terminates the subprocess on timeout. Idle continuation uses bounded per-session generations and only the
asynchronous SDK request `promptAsync({sessionID, messageID, parts})`. The
caller-owned message identifier distinguishes the injected callback from
external user activity; no synchronous prompt fallback exists.

OMP's generated `.omp/extensions/reconc.ts` is also transport-only. It
validates and forwards documented `ExtensionAPI` events, uses `tool_call` and
awaited main-agent `session_stop` as its fail-closed boundaries, and keeps
approval events observational because OMP does not accept decisions from
them. `tool_result.isError` is the authoritative outcome; successful built-in
Bash results alone synthesize exit code zero. The adapter uses the same
session-owned worker, one-shot recovery, combined output, UTF-8, timeout, kill,
and wrapper-resolution contract as the other Bun adapters. Session shutdown has a one-second Reconc route budget
inside OMP's two-second extension-handler budget.

Pi's generated `.pi/extensions/reconc.ts` is a trust-gated transport adapter.
It uses awaited `tool_call` and `user_bash` as fail-closed boundaries while
tool results, lifecycle, compaction, and shutdown remain observational.
`tool_result.isError` is authoritative; successful built-in Bash results alone
synthesize exit code zero, while failed Bash output never becomes a fabricated
exit status. `agent_settled` can request a bounded continuation through
`sendUserMessage`, but the host API returns no delivery acknowledgement. Pi
exposes `project_trust` as a project-level veto, which Reconc reads rather than
answers, and no per-call permission event, MCP discriminator, post-`user_bash` result, or
synchronous Stop gate. Host cancellation wins over Reconc subprocess output.
The Pi shutdown route closes its repository worker after recording SessionEnd.

### Path-traversal

Every path in the payload is:
1. Joined with the project root.
2. `filepath.Clean`'d.
3. Resolved to its operating-system identity at the longest existing ancestor.
   Unix symlinks and Windows reparse points, including directory junctions,
   are followed; Windows 8.3 aliases are expanded.
4. Tested for containment in the resolved project root before the missing
   suffix is created.
5. Rejected with `RepoBoundaryError` if outside.

Prospective hook-install targets, agent-session repository identities, evaluator
evidence paths, `require_script` targets, and assurance inputs share
`internal/pathidentity`; each caller retains only its bounded purpose-specific
containment policy.

`require_script` splits the two checks it needs. Containment is enforced on the
resolved parent directory, because a path whose every segment is a plain name
can still leave the repository through an intermediate directory symlink, which
no lexical `..` rejection sees. The script leaf stays lexical so `execfile.Is`
keeps refusing a symlinked script file. A directory symlink that resolves back
inside the repository remains a legal layout.

### Command-injection

Words are resolved to the literal strings a shell would hand to `execve`
before any comparison: quotes are removed and backslash escapes are applied, so
`\rm`, `r''m`, and `"rm"` all compare as `rm`. ANSI-C quoting (`$'\x72\x6d'`)
is not decoded and is reported as a dynamic word instead, which makes callers
fail closed rather than compare text that is not what runs. Deny matching also
folds the case of the program name, because `RM` and `rm` name the same program
on the case-insensitive filesystems this product supports; evidence matching
stays case-sensitive so a claim is only satisfied by the command the author
named.

Static `eval` follows the POSIX two-pass boundary exactly. After the outer
parse removes syntactic quotes and resolves supported escapes, `eval` joins its
resulting arguments with one space and reparses that command. Outer quote
boundaries are therefore intentionally not restored; only literal quote or
backslash bytes passed as argument data can group or escape the nested parse.
Dynamic expansion, unsupported quoting, parse failure, and exhausted recursion
remain incomplete and fail closed. Reconc performs both parses in-process and
never invokes a shell for analysis.

Command / tool-use strings in the payload are **data**, never
executed by reconc. The evaluator's rule-matching compares them as
strings; no `exec.Command` call path in the runtime handlers takes
user data as the binary name or unescaped argument. Enforced by a
source-scan guard test (`TestNoExecCommandTakesNonLiteralBinary` in
`internal/runtime/agentsession`): every exec.Command binary in the
payload-handling package must be a string literal.

### Session identity boundary

- The host-provided session ID is validated exactly (non-empty, at most 512
  bytes, no surrounding whitespace or control characters) and mapped to a
  collision-resistant storage key; sanitized-name collisions cannot alias two
  sessions.
- Every loaded state file must match the requested session ID, canonical
  repository root, and computed report path. Mismatches, oversized input, and
  corrupt JSON fail closed instead of being replaced with empty state.
- Session state, active pointers, reports, and lock files use private
  permissions, bounded reads, atomic publication, and cross-process locks.
- Legacy sanitized paths are migrated only after the same identity checks.

Reconc does not generate, authenticate, HMAC, or expire host session IDs, so a
hostile process with the same user and filesystem authority remains outside
this boundary.

### Atomic publication identity boundary

`internal/atomicfile` binds every existing and newly created parent component
to an opened `os.Root` directory identity before placing a temporary file.
The same rooted directory performs target comparison, temporary-file cleanup,
replacement or hard-link publication, and parent synchronization. Parent
identity is revalidated before mutation and after publication; a component
that becomes a symlink, non-directory, or different inode fails closed.
Existing targets are opened as non-symlink regular files. Byte comparison and
mode reconciliation use that opened file, then revalidate its path identity,
so a substituted symlink target is never chmodded. Public writes create
missing parents with `0755`; state-bearing callers use the explicit private
API and create them with `0700`. On Unix, each newly created component is
committed by syncing its already bound parent before traversal continues. The
macOS `/var`-style filesystem-root alias
is canonicalized before binding, while nested publication symlinks remain
rejected. Unix uses rooted rename plus directory fsync; Windows uses rooted
replacement and flushes the temporary file before publication. Windows then
reopens that temporary relative to the bound `os.Root` with
`FILE_WRITE_THROUGH` and renames the handle through `NtSetInformationFile`
relative to the same open directory handle. This preserves parent identity
without reconstructing an absolute path; the legacy rooted rename information
class remains the fail-closed compatibility fallback.

Bootstrap create-only publication adds an ordered entry protocol around this
boundary. It syncs the staged payload and parent, publishes by hard link or
exclusive copy, syncs the target parent, removes the reserved stage, and syncs
that removal. Rollback applies the same parent barrier to owned target and
directory removal. Windows retains payload `File.Sync`, rooted identity checks,
and create-only semantics but does not claim unsupported directory fsync.

### Private state filesystem boundary

`internal/privatefs` is the shared boundary for Reconc-owned state directories,
lock files, and private marker/proof publication. It creates missing components
one at a time with private security and identity-checks traversed existing
directories without changing their mode or ACL. The final directory must pass
owner, mode, identity, and ACL/security-descriptor validation; the explicit
repair path changes only that final boundary through its opened descriptor.
Lock creation opens the exact regular inode with create-only semantics, applies
private mode/security through the descriptor, validates single-link ownership,
and revalidates the directory entry before returning it. The absent and
existing paths are separate rooted operations: absence uses exclusive creation,
while an `ErrExist` race is reopened only after non-symlink, regular-file,
identity, and link-count validation. Rejected leaf or parent replacement is
never followed, and a newly created file is removed through the still-bound
parent if parent validation fails. On Windows, ACL publication reopens the
bound object with only `WRITE_DAC|WRITE_OWNER` and calls handle-based
`SetSecurityInfo`; replacement paths and reparse targets cannot receive the
security mutation. Action state, installation receipts, retention, command
proofs, and unresolved policy proofs
use this boundary; their paths, filenames, retention policy, and public JSON
contracts are unchanged.

Same-directory binary transaction files use the file-only form of this
boundary. It secures and revalidates the already opened temporary identity
without changing or requiring a private parent directory: Unix enforces mode,
owner, and link count, while Windows persists and validates a protected
current-user-only DACL through a handle reopened from that identity.
Replacement tests accept only two safe outcomes: the opened identity detects
the replacement, or the operating system refuses the
replacement while that identity is open.

The audit layout is a private specialization of the bounded JSONL contract:
`.reconc` is `0700`; live and archive evidence, the detached head, lock,
append journal, and temporary backups are `0600`; and every member is checked
through the private filesystem security boundary before read or mutation.
Legacy `0755`/`0644` audit state is migrated in place only after identity and
regular-file checks; a symlink, special file, wrong owner, or invalid hard-link
state is rejected without deleting evidence. JSONL hash-chain, archive count,
size bounds, and public CLI output remain unchanged. Retention inspects the
validated ring under the same audit lock before reporting any cleanup. Same-
process append bursts pass through a per-audit-directory mutex before the
bounded cross-process lock, avoiding lock-polling storms while preserving the
file lock as the inter-process authority.

JSONL live-file publication uses the same explicit state split under its rooted
parent. A missing live name is opened with exclusive creation; a regular file
that wins the race is reopened without create permission and must retain the
same identity and one directory link before append. Dangling symlinks,
hard-link aliases, leaf replacement, and parent replacement therefore fail
before record bytes are written or an out-of-tree target can be created.

The action ledger retains full-chain verification as the startup, recovery,
external-writer, and explicit-read authority. After one successful verification,
same-store appends may advance an HMAC-authenticated checkpoint containing the
detached head/tail binding, exact retained-file change generations, active-call
records, and a rolling terminal-call digest/count. The in-memory terminal-call
membership index is rebuilt only from verified retained records and is never
accepted from disk. Any file-set, OS change-generation, checkpoint-generation,
repository, or key mismatch falls back to full verification. Detached-head and
checkpoint publication remain commit steps of the JSONL journal, so interrupted
publication is rebuilt only after recovery verifies the retained chain.

### Production lock acquisition and ordering

No production path calls the raw blocking `filelock.Lock` or `RLock` APIs.
Immediate probes use `TryLock`/`TryRLock`; operational locks use the bounded
`LockContext`/`RLockContext` loop and return typed cancellation or timeout
errors. Unix lock, try-lock, and unlock syscalls retry `EINTR`; context-bound
acquisition checks cancellation and deadline between interrupted attempts while
contention still waits on the existing polling interval. Windows lock semantics
are unchanged. The legacy APIs without a context use the explicit ten-second
default;
the hook-runtime state boundary uses a thirty-second contention budget because
its concurrent writers perform bounded JSON/state work under the lock.

The lock order is one-way: Stop-policy report lock -> session lock; repository
run state descriptor -> run-decision JSONL lock. Receipt, audit, retention,
TASK lifecycle, command-proof, compiler, and bootstrap locks are independent
transaction boundaries and are never acquired while holding one of those
other class locks. Command-proof retention runs only after its capture lock is
released. Unlock and close errors are joined with the operation result.
Directory durability helpers likewise join fsync and close failures, so a close
failure can never replace the original durability failure.

### Incremental Stop decision cache

One-shot hook processes always use the exact Stop fingerprint. A persistent
session worker additionally owns an isolated, memory-only cache of at most 64
repository/session generations and a separate 16 MiB aggregate cache of
verified evidence-segment prefixes. Prefix hits reread each bounded segment
through an identity-stable regular-file snapshot and compare its exact byte
hash before reusing decoded immutable evidence; segment count, chain head,
linkage, and the current state revision remain mandatory. After an exact successful report for a costly
dirty state, a repeated Stop may replace dirty-file content hashing with
conservative generation samples. The generation binds canonical root identity,
Git status, HEAD and index entries, platform file identity and change time,
recursive untracked-tree metadata, policy lock and source identity, every
reachable policy-declared input, typed TASK state, schema configuration, and
session evidence. It never starts a watcher, daemon, or background lifecycle.

Generation reuse is enabled only when the exact dirty state contains at least
16 MiB or 1,024 entries. Small states, one-shot routes, dirty submodules,
oversized content, unsupported file metadata, malformed Git or TASK state,
bounded-tree overflow, and any identity uncertainty use the exact path. Normal
untracked-directory sentinels are recursively content-and-metadata hashed under
a 100,000 entry and 64 MiB aggregate-content bound, so nested edits cannot
retain a constant directory identity. File replacement during exact hashing
and directory replacement during traversal fail closed. Generation state is
sampled around report loading and revalidated after the final evidence reload.
The existing per-session report lock serializes equivalent Stops. Evidence or
exact cache-input mutation during evaluation triggers a current-state retry;
three consecutive unstable evaluations fail closed. A concurrent follower
therefore either reads a fully published report whose bindings still match or
evaluates current state.
Within one Stop attempt, the bounded policy-lock scan is owned by an
attempt-local cache keyed by the canonical repository root, normalized write
paths, and the scan's exact lockfile SHA-256. Fingerprint construction,
cacheability, generation capture, expiry, assurance inputs, and generation
storage reuse that immutable result. A final bounded byte-hash comparison is a
post-observation barrier; lock mutation or unreadability invalidates the scan
and prevents report reuse or cache publication. No scan state crosses workers
or repositories.
Each attempt phase also carries a typed `stopPolicyAttemptSnapshot` containing
the complete session-evidence revision, Git view, TASK view, policy-source
digest/count, and lock-scan identity. The explicit `before_evaluation`,
`after_evaluation`, and `before_cache_publication` boundaries exchange these
snapshots; generation comparisons therefore
cannot accidentally combine source, Git, or TASK observations from different
capture times.

Applicable native-assurance rules always bypass report reuse. Their complete
globbed authority surfaces and wall-clock-aged proof inputs are intentionally
richer than the fixed path identity set. Completion nevertheless samples a
deterministic native-assurance input identity before and after evaluation. That
identity covers exact loaded bodies, bounded directory observations,
applicability, derived facts, findings, and time-dependent verdicts, so a moving
authority surface invalidates the candidate rather than reusing a Stop report.
`require_script cache_inputs` is a
trusted author assertion over content and the supported mode, size,
modification-time, and platform change/identity metadata; scripts that depend
on any ambient or unsupported input must omit it and therefore run each time.

### Run state concurrency and Stop routing

`.reconc/run/state.bin` uses two CRC-protected slots and is published only for
material transitions.
Disabled hook events do not create run state or decisions. Enabled
continuations append decisions for the first observation, material progress,
the third and fifth unchanged nudge, the no-progress release, and terminal or
checkpoint transitions. Equivalent observations between those points do not
force-sync duplicate JSONL records. Parallel agent tool calls can still spawn
concurrent `reconc hook runtime` processes, so three invariants keep an active
run from being silently disabled:

- **Crash-safe fixed layout.** Each state update writes a fixed 88-byte payload
  into the inactive 512-byte slot with a monotonic sequence and CRC32C over
  both header and payload. The
  payload stores timestamps as integers, the progress digest and canonical
  repository-root identity as 32 raw bytes each, and the disable reason as a
  bounded enum, so the hot read does not decode
  variable strings. Readers select the newest valid slot; a torn write leaves
  the previous slot intact and never decodes as disabled. A material update
  synchronizes the complete inactive slot before unlock, while a no-op performs
  neither write nor sync; write, sync, unlock, and close errors are joined.
- **Locked read-modify-write.** `mutateRepositoryRunStateResolved` /
  `withRepositoryRunFileResolved` serialize load->mutate->save by locking
  `.reconc/run/state.bin` itself, mirroring `MutateSessionState`,
  so concurrent mutators cannot lose each other's updates. The Stop hook's
  own terminal-disable and continuation decisions run through the same locked
  mutator, so a concurrent `run off` cannot be lost. The append-only
  `decisions.jsonl` log uses a separate cross-process
  lock and a 2 MiB plus two-archive ring, so state-lock re-entry cannot deadlock
  decision publication.
- **Session-isolated guard.** Each continuation updates its external session
  state before the durable run-state mutation. No run-state lock is held while
  taking a session lock. State reads and writes serialize on that session lock,
  while the repository-wide active-session pointer has its own lock acquired
  only after a session lock. Each session owns its progress digest and six-stop
  counter, preventing deadlocks, Windows sharing violations, and cross-session
  budget interference.

Repository mode is controlled normally by `reconc run on|off`; `run reset` is
the recovery-only path for corrupt, unsupported, or foreign-root state and
preserves the decision log. Prompt text,
runtime interrupts, session lifecycle events, runtime changes, compaction, and
application restarts never mutate its locked state. An explicit interrupt
releases only the current host invocation. `run off` is the only normal manual
disable action; complete or absent TASK state disables it automatically after
the terminal gates.

Stop reads TASK state through `tasklifecycle.InspectRunState`. Executable
`continue` and `claim` dispositions return the runtime-native continuation
response before policy report construction and without a Git process.
`blocked`, `complete`, and `absent` dispositions continue to the terminal Stop
gate; `invalid` fails closed. This fast path never bypasses PreToolUse, TASK
policy, or terminal evidence gates. The TASK lifecycle package shares one
identity-aware path guard across configuration, overview, runtime-state, and
detail reads. It rejects links, reparse/irregular components, and non-directory
intermediates, stores verified filesystem identities rather than path strings,
and revalidates the snapshot after each fast-path detail read and before
accepting the final overview observation.
mutation transactions, pre-commit, or terminal policy enforcement. The
no-progress guard compares typed TASK state plus a write/command material-event
counter inside the locked state of that exact session; reads, unrelated events,
and concurrent sessions cannot fake or reset progress. At six events,
repository mode releases one Stop and resets only that session while leaving
the explicit durable switch enabled. Strict Grok Stops bypass this guard and
use the separate 32-delivered-interjection cap. Repository runs leave the fast
path only after 64 new material events, 30 minutes with new progress, or a
failed command, then reuse the normal full Stop report as a policy checkpoint.
Explicitly configured TASK state fails closed if its overview disappears, and
optional committed completion reuses that terminal report's Git and typed TASK
snapshot instead of inspecting either control plane again.
The shared `completiongate` used by Stop-facing views, `done`, and TUI binds
that snapshot to current policy, session evidence, staged command proofs, and
typed TASK completion. Snapshot construction first derives the exact
`runtime.ExecutionInputs` that evaluation will consume: Git dirty paths and
relativized epochs when Git is available, otherwise session paths and epochs,
plus staged command proofs loaded at capture time. Valid template captures are
resolved with evaluator first-match semantics and bind every concrete evidence
or freshness target. The snapshot also binds temporal freshness and native
assurance authority identities. It captures all of that state again after
evaluation and rejects races. A same-candidate explicit block is
stored outside the repository until a later validated explicit pass clears it;
retention cannot manufacture a pass.

Every Git subprocess that contributes command-proof, completion, Stop,
agent-session, memory-worktree, alias, or runtime-diff evidence is constructed
by `internal/gitexec`. That boundary removes every ambient `GIT_*` variable,
pins system/global config, prompts, optional locks, pager, hooks, fsmonitor,
untracked cache, and locale to inert values, then binds repository discovery to
the requested working directory. Repository Sync uses the same contract and is
the only caller allowed to add its explicit ephemeral object directories.

`BenchmarkRepositoryRunStopHotpath` measures the in-process executable-TASK
continuation path without process startup. A five-iteration Apple M1 sample on
2026-08-05 measured 14,264,267 ns/op, 67,268 B/op, and 571 allocs/op. The
process-independent no-op persistence benchmark
`BenchmarkDuplicateSessionMutation` measured 256,433 ns/op, 16,496 B/op, and
176 allocs/op in the same run. Normal audit append uses a bounded tail record
plus the detached chain head instead of replaying every retained entry;
`BenchmarkAuditAppendRetainedChain` measured 34,346,403 ns/op with no retained
entries and 33,281,819 ns/op with 200 retained entries over three iterations.
These are reproducible observations, not latency contracts. The routine paths
start no Git process; fsync-backed state, pointer, decision-log, and audit
durability dominate the remaining cost.

Calibrated benchmark history covers twelve allocation-sensitive paths in the
action evaluator, action ledger, inspection, compiler, hook worker, source
loader, path resolver, and runtime evaluator. `make
benchmark-record` runs five samples at 100 fixed iterations with one logical
CPU, records the raw medians plus Go version, OS, architecture, CPU identity,
commit, dirty state, and benchmark parameters, and normalizes every target
against a same-package reference from the same run. `make benchmark-compare`
accepts only the same suite, Go version, OS, architecture, sample count,
benchtime, and logical CPU count. CPU models may differ because the ratios are
same-run calibrated; this reduces host noise but does not make different
machines equivalent. Absolute medians remain visible and informational.

The checked baseline allows at most 20% normalized time growth and 5%
normalized bytes or allocation growth. `make benchmark-baseline
CONFIRM_BENCHMARK_BASELINE=1` is the only baseline-refresh path and requires a
fresh `make benchmark-record`; comparison never rewrites either input. The
manual `Calibrated Benchmarks` workflow records and compares on macOS. Normal
push and pull-request verification never starts the benchmark suite, so these
measurements inform deliberate performance review without becoming noisy
wall-clock CI gates.

### Causal command-success evidence

Session state advances a monotonic evidence epoch for each write tool event.
Every written path stores its latest epoch, and each command outcome stores the
current epoch. `require_command_success` accepts a matching success only when
its epoch is at least the newest epoch among the rule-triggering writes. A
later relevant write therefore requires a rerun, while an unrelated later
write does not. Legacy session state with unordered writes and command results
is upgraded fail-closed by placing its writes one epoch ahead. Explicit
`--command-success` evidence uses the maximum epoch because it asserts the
complete evaluation snapshot.

Staged commit gates use a stricter boundary. `reconc exec --staged` owns the
real child process and publishes a tamper-evident success receipt bound to the
canonical repository, HEAD, index tree, exact command, execution mode, exit
code, and timestamps. `ci --staged` accepts only a current receipt for that
same Git candidate and ignores mutable agent-session command outcomes. Parallel
snapshot capture is cross-process serialized and transient Git index locks are
retried for two seconds. When an active session exists, `reconc exec` records
the outcome at the current causal evidence epoch with `reconc-exec` provenance.

Ordered JSON `events` derive the same epochs during ingestion. Check reports
publish optional `write_epochs` and `evidence_epoch` fields so the decision is
auditable without expanding the normal zero-value payload.

### require_command_success redirect tolerance

`matchingCommandResults` matches a recorded command against a
`require_command_success` rule by normalized equality
(`normalizeCommandSemantics`: RTK-prefix strip + absolute-repo-path cd
folding), then additionally strips trailing shell output redirections
(`stripTrailingRedirects`: ` 2>&1`, ` >file`, ` >>file`, ` 2>err`, ` <in`)
from the recorded side. So a rule authored as `cd x && go test ./...` is
satisfied by a recorded `... 2>&1` or `... > out.log`. Pipes are
deliberately not stripped - a pipeline's exit status is the last stage's,
so `go test ./... | tail` could record success even when the test failed -
and extra arguments are not stripped by default, so the matched command is
the same command that succeeded. Rules may opt into token-boundary prefix
matching via `command_match: prefix` (RECONC-0004); without it,
`forbid_command`/`require_command` (`matchingCommands`) keep exact
normalized matching. An unqualified expected executable also matches the
basename of an absolute executable path, while explicitly path-qualified rules
remain exact. PreToolUse command prevention additionally walks quote-
aware executable shell segments including groups and process substitutions,
folds unquoted backslash-newline continuations, skips leading redirections,
resolves common wrappers and command launchers without treating ordinary
arguments or comments as executable positions, and fails closed on dynamic
executable names or exhausted bounded nested-shell analysis. During
PreToolUse, a composite violation is blocking only when the current command
itself hits a direct `forbid_command`; historical command evidence and other
failing composite subchecks cannot poison a later safe command.
The default destructive-command guard additionally resolves inline and
configured Git aliases with bounded process output, timeout, and recursion.
Unknown subcommands or aliases whose executable shape cannot be proven fail
closed before Git executes them.

The RTK-prefix strip in `normalizeCommandSemantics` is a compatibility
shim for transparent CLI proxies: a command recorded as `rtk go test
./...` satisfies a rule authored as `go test ./...` because the proxy
preserves the wrapped command's semantics and exit status. It is not a
coupling to any specific tool beyond recognizing that prefix.

### Resource exhaustion

- `ResourceLimitedJSONReader` wraps stdin: bails at 64 MiB + 5s
  timeout + 32-level depth.
- Session evidence has per-field item and byte caps plus a 1 MiB serialized
  ceiling. Overflow persists a fail-closed marker used by PreToolUse and Stop.
- Action inspection bounds canonical values to 8 MiB, strings to 4 MiB,
  nesting to 32, JSON items to 65,536, output schemas to 1 MiB and 8,192
  items, results to 4,096 content blocks, decoded binary blocks to 3 MiB, and
  phase work to 500 ms pre-call, 1 second post-result, or 250 ms progress.
- Action-ledger records are capped at 64 KiB. Its live file and each of two
  archives are capped at 4 MiB, its detached head at 8 KiB, its authenticated
  incremental checkpoint at 16 MiB, and append transactions at ten seconds.
  An active-call retention conflict fails the
  append instead of pruning the call's retained beginning.
- The MCP gateway caps protocol frames and results at 10 MiB, arguments at
  8 MiB, tools at 512, active calls and pending approvals at four each,
  progress at 128 events and 1 MiB per call, and retained child stderr at
  256 KiB. Overflow fails closed without forwarding raw content.
- Audit and run-decision JSONL writes rotate before append through fixed archive
  rings. Live-file creation, each archive mutation, and journal/backup cleanup
  validate one bound parent and establish either a Unix directory durability
  barrier or the rooted Windows ordering boundary before advancing. Interrupted
  rotation is recoverable from every committed ring mutation. Lifecycle
  retention bounds sessions, reports, locks, staged command proofs, the product-wide
  project-root set, generated binaries, and owned temp residue outside the Stop
  path.
- Native assurance source gates scan matching changed files only. Layout and
  substantive-proof authority gates inspect their complete configured surface;
  unreadable or over-budget authority fails closed.
- One native-assurance evaluation owns normalized changed paths, validated glob
  decisions, canonical filesystem identities, bounded body snapshots, line
  indexes, BOM-aware JSON manifest objects, and shared Go syntax/format facts.
  Large Go file sets may schedule CPU-only facts through at most four workers
  after deterministic body reads and budget claims; gates still consume files,
  findings, and operational errors in stable sorted and declaration order.

### Secrets in audit

Tool-use `command` strings may contain API keys or tokens as
arguments. Default audit-log record only stores the FIRST token of
the command (e.g. `"go"` not `"go test -api-key=sk-..."`).
`RECONC_AUDIT_VERBOSE=1` opts into full command strings for debugging.

The Action Ledger has no verbose payload mode. Its closed event types cannot
carry raw arguments, results, headers, credential or environment values,
stderr, prompts, or arbitrary MCP metadata. Policy-selected values use only
repository-bound, domain-separated keyed identities and explicit completeness.

### Dependency review

reconc's non-stdlib dependencies processing the payload:
- `gopkg.in/yaml.v3` (YAML source parsing, not payload — irrelevant
  to this threat model).
- `github.com/pelletier/go-toml/v2` (strict syntax validation for the explicit
  Kimi Code global hook lifecycle; it does not decode runtime hook payloads).
- `github.com/bmatcuk/doublestar/v4` (compile-time glob syntax validation and
  differential contract oracle; runtime action matching uses immutable
  precompiled tokens, with no eval).
- `mvdan.cc/sh/v3/syntax` (bounded AST parsing of untrusted shell text for
  command matching only; parsed input is never executed and unsupported or
  over-deep executable structure fails closed).
- `github.com/Microsoft/go-winio` plus `golang.org/x/sys/windows` (Windows-only
  named-pipe dialing and enumeration for Grok leader IPC, plus Windows Job
  Object ownership for gateway child trees; no network listener).
- `github.com/modelcontextprotocol/go-sdk` (official pinned MCP `v1.7.0`
  protocol/session implementation behind `internal/mcpgateway`; Reconc owns
  strict frame bounds, canonical arguments, policy orchestration, result
  inspection, and process lifecycle).
- `github.com/santhosh-tekuri/jsonschema/v6` (offline Draft 2020-12 compilation
  and validation for declared MCP output schemas; remote references are
  rejected, patterns use the bounded RE2-compatible subset, and no loader
  performs network IO).
- `golang.org/x/text/unicode/norm` (deterministic Unicode normalization before
  bounded local detector matching).

Repository hook payload decoding uses stdlib `encoding/json` behind Reconc's
depth-limited reader. MCP frames additionally pass through the pinned official
SDK only after Reconc's strict bounded frame reader and before independent
canonical argument, schema, policy, and result validation. The Go 1.27
`jsontext` scanner performs the strict UTF-8, duplicate-name, depth, item,
string, number, root, and trailing-data pass once while exposing borrowed root
envelope slices to observers. Only fields that escape the read callback are
cloned. Reader and writer storage is cleared between frames and retained only
up to 256 KiB; transformed frames are rescanned only when their bytes changed.

### What this threat model does NOT cover

- A compromised reconc binary itself (trust root).
- A deliberately hostile same-user process or agent replacing repository
  policy, hooks, state, or binaries; fabricating self-reported evidence;
  disabling host hooks; or bypassing Git hooks. External sandboxing and
  protected remote CI or branch rules must own that adversarial boundary.
- Kernel-level attacks (e.g. PID reuse allowing session-state
  tampering between runs). Out of scope; we assume the OS is sound.
- Network-borne attacks against Reconc's policy compiler/runtime (the core is
  offline and has no network surface). Optional Grok ACP execution belongs to
  the external Grok process, which owns its inference network, authentication,
  sandbox, and provider security boundaries. Leader steering uses only
  same-machine Unix sockets or Windows named pipes.
