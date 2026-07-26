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

`compile` stops at the lockfile. `check` / `ci` / `assert` / `can`
load the lockfile and run the runtime evaluator. `fix` / `explain`
also use the runtime then render the result. `done` binds the evaluated
candidate through `completiongate`; `proof` renders that same candidate through
`proofbundle`. `why` and `diff` inspect compiled lockfiles, while `watch`
observes policy sources and recompiles explicitly when they change.
Host-native MCP events and configured generic MCP identities enter through the
same compiled lockfile. Exact selectors classify a call as repository read,
repository write, command, or external before session evidence is considered;
unclassified or malformed calls never become positive repository evidence.

## Package map

```
buildprovenance/ deterministic target/source identity + byte-only binary inspection
internal/
  adopt/          convention detector, rule suggestions, and stack-pack recommendations
  agentguide/     embedded agent-integration guide + section lookup
  assurance/      bounded native layout/source/manifest/proof gates
  atomicfile/     write-on-change and atomic publication primitives
  audit/          append-only JSONL decision log + rotation + stats
  bootstrap/      deterministic install/remove transactions + receipts + binary resolution
  changelog/      docs/changelog.md rotation into quarterly archives
  cli/            command dispatch plus responsibility-owned command modules
  commandmeta/    canonical dependency-neutral command, flag, help, and output contract
  commandproof/   staged candidate-bound command-success receipts
  compiler/       lockfile builder: digest, writer, conflicts, migrations, lock
  completion/     bash / zsh / fish completion generators
  completiongate/ exact final-completion report over policy, candidate, evidence, and TASK state
  contextsize/    token-budget guard for canonical entrypoints + active TASK
  demo/           isolated real-command product journey + self-digested proof result
  errors/         typed exception hierarchy (PolicySourceError, LockfileError, ...)
  execfile/       cross-platform regular-file and executable validation
  extractor/      prose-to-rule heuristic scanner (regex-only, no LLM)
  grokacp/        strict Grok ACP stdio client + cross-platform leader IPC stop steering/probing
  hooks/          typed registry + generators + install/uninstall + activation + scaffold sync
  ingest/         discovery + source loading (AGENTS.md, .reconc.yml, presets, globals)
  lockdiff/       structural lockfile comparison (ignore-provenance semantics)
  filelock/       cross-platform process locks
  jsonl/          bounded locked JSONL append + archive rings
  manpage/        groff reconc(1) generation from the canonical command table
  parser/         YAML-to-Rule validation + template expansion + scope expansion
  pathidentity/   Unix symlink + Windows reparse/8.3 filesystem identity
  policy/         Rule / Scope / Source / Kind / Mode types
  policyproof/    tamper-evident unresolved policy-decision receipts
  presets/        bundled policy packs (embed.FS) + user overlays
  proofbundle/    deterministic portable JSON and Markdown completion evidence
  repositoryignore/ canonical target-repository runtime-ignore contract
  runtime/        evaluator + remediation + git integration + subprocess runner
  retention/      bounded runtime storage lifecycle + owned temp cleanup
  runtime/agentsession/  hook payload handlers, session evidence state,
                  stop policy cache, run-state store (the package the
                  hook-runtime threat model below describes)
  scaffold/       reconc init implementation
  schema/         canonical public JSON contract URLs + enterprise override
  shellcommand/   bounded shell parsing and executable-command discovery
  stackdetect/    shared bounded manifest/source stack discovery
  tasklifecycle/  typed TASK profiles + recoverable state transactions
  templates/      bundled rule-shape templates (embed.FS) + user overlays
  tui/            dependency-free terminal dashboard
```

`cmd/reconc/main.go` parses argv, delegates to
`cli.Run`, and translates the returned error into an exit code.
Within `internal/cli`, `cli.go` owns only public errors, top-level
dispatch, and canonical usage. Compile, evaluate, inspect, explain, CI,
bootstrap, scaffold, source analysis, quality, maintenance, catalog, metadata,
hook, workflow/session, TASK lifecycle, repository-run, and deep-doctor logic
live in responsibility-owned files without a second router or compatibility
wrapper.
Hook generation
is separate from merge/install logic, runtime lockfile trust is separate from
rule evaluation, and Stop decisions are separate from general session event
handling.

## Key invariants

1. **Byte-stable portable lockfile.** Two compiles of identical sources in
   different clones or worktrees produce identical bytes. Format 2 uses `.` as
   its repository/discovery root marker. Compiler emits canonical JSON (sorted keys,
   indent-2, trailing newline). Source digest is SHA-256 over the
   same canonical form. Enables rsync-style drift detection and
   git-friendly diffs.

2. **Fail-closed on tampering.** Unknown document or rule field, unknown rule kind, malformed YAML,
   stale lockfile, non-portable current root marker, unsupported schema URL --
   every degradation path raises a typed error rather than silently
   treating the situation as "pass".

3. **Owned publication.** Write paths publish atomically or through an explicit
   transaction. Canonical lockfile bytes are compared before publication, so an
   unchanged compile performs no filesystem write. Bootstrap install is
   create-only, emits candidate files for drift, and rolls back only
   transaction-owned unchanged files. Bootstrap removal is receipt-bound,
   SHA-verifies owned files, strips only managed blocks, and preserves drift.
   Hook merges and uninstalls preserve
   unrelated host configuration. Bounded JSONL writers rotate under a process
   lock before append. Write, sync, close, unlock, and CLI output failures are
   propagated instead of being reported as successful publication.

4. **Explicit side effects.** Compile, bootstrap, hook installation, TASK
   mutation, retention, and hook event handling own their documented files.
   Read-only commands never refresh policy. `RECONC_AUDIT=1` is still required
   for the optional decision audit log.

5. **Advisory compile lock.** `.reconc/.compile.lock` is a reusable OS-backed
   exclusive file lock. A second compiler fails immediately, process exit
   releases ownership automatically, and no timestamp-based stale reaping can
   steal a live lock.

6. **Satisfiable conflict analysis.** Static command contradictions follow
   runtime `require_command` semantics: any configured alternative can satisfy
   the rule. A forbid/require pair is reported only when their exact trigger
   scopes overlap and one forbid rule blocks every required alternative.

7. **Exact MCP identity and effect.** MCP classification uses the complete
   `(platform, server_fingerprint, tool)` key. Fingerprint presence never
   falls back to a weaker selector. Effect-specific RFC 6901 fields must resolve
   to typed repository-contained paths or exact commands before policy or
   evidence handling. Unknown identity, malformed input, and unknown outcome
   are non-evidentiary.

## Key external contracts

- **Lockfile schema** (`$schema` in policy.lock.json): bumped only on
  shape-breaking changes. Migration chain in `compiler/migrations.go`
  walks older versions forward.

- **CheckReport / CompletionReport / FixPlan schemas**: same policy. Additive changes
  (new optional fields) don't bump the version; breaking changes do.

- **Published schema documents**: the six immutable
  `schemas/v1/*.schema.json` contracts and the current
  `schemas/v2/policy-lock.schema.json` are canonical Draft 2020-12 documents,
  use format-versioned repository URLs as `$id`, and ship in the checksummed
  release inventory. `policy-config.schema.json` is the strict authoring
  contract; the v2 lock schema describes current portable lockfiles, while the
  v1 lock schema remains the validated migration input.

- **MCP authoring and lock contract**: `mcp.unclassified` is `host` or `deny`;
  tool mappings use the typed platform, optional SHA-256 server fingerprint,
  exact tool, effect, and effect-specific JSON Pointers. The authoring and v2
  lock schemas reject unknown fields and invalid cross-field combinations.

- **Exit codes 0/1/2**: stable across all subcommands for agent
  consumption. 0 = pass or warn, 1 = runtime/input error, 2 = at
  least one blocking violation.

- **Demo result format 1**: `reconc demo --json` records real child-command
  arguments, exit codes, decisions, durations, proof artifact hashes, cleanup
  state, the verified completion digest, and a self-digest. It never treats
  rendered text as proof.

- **Public runtime env vars** (`NO_COLOR`, `RECONC_HOME`, `RECONC_AUDIT`,
  `RECONC_AUDIT_VERBOSE`, `RECONC_CLAUDE_STATE_DIR`,
  `RECONC_SCHEMA_BASE_URL`, `RECONC_STOP_FINGERPRINT_UNTRACKED`, and
  `RECONC_GROK_STEER`): stable names. Adding a new one is additive; renaming or
  removing needs a major version bump. Debug and installer variables are
  catalogued separately in `docs/commands.md`.

## Request flow example: `reconc check --write src/x.go`

1. `cli.Run(argv, version, stdout, stderr)` dispatches to `runCheck`.
2. `runCheck` builds `runtime.ExecutionInputs` from flags, captures
   `start := time.Now()`.
3. `runtime.CheckRepoPolicy(repo, inputs)`:
   - `ingest.DiscoverPolicyRepo(repo)` walks up for `.reconc/`,
     `.reconc.yml`, `AGENTS.md`, etc.
   - `internal/runtime/lockfile.go` reads and validates schema, version,
     repository root, migration state, and source freshness.
   - Normalises the input paths against the repo root.
   - For each rule in the lockfile: applies the scope filter
     (`ruleScopeMatches`), then dispatches to the per-kind
     evaluator (`evalDenyWrite`, `evalRequireRead`, ...).
   - Collects violations, calls `report.Finalize()` which derives
     decision / counts / actions / rule_ids.
4. `maybeAudit("check", report, start)` appends one JSONL entry iff
   `RECONC_AUDIT=1`.
5. Output is rendered: terse / json / text depending on flags.
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
        │
        ├──► hooks
        ├──► bootstrap ──► stackdetect
        ├──► adopt ──► stackdetect, presets
        ├──► extractor
        ├──► lockdiff
        ├──► audit
        ├──► changelog
        ├──► contextsize
        ├──► commandproof
        ├──► completiongate ──► commandproof, policyproof, runtime, tasklifecycle
        ├──► demo ──► runtime, completiongate, proofbundle
        ├──► proofbundle ──► completiongate, commandproof, policyproof, schema
        ├──► retention
        ├──► tasklifecycle
        ├──► agentguide (embed)
        ├──► templates  (embed)
        ├──► presets    (embed)
        ├──► scaffold ──► repositoryignore
        └──► completion
```

`commandmeta` imports no product package, so CLI, completion, and manpage share
the public surface without a cycle. Nothing below `cli` imports `cli`. The compiler does not know about the runtime;
the serialized lockfile is the boundary. `schema` is the single owner of public
contract URLs. Runtime lockfile loading imports compiler only for registered
migrations, current-envelope validation, and source-digest freshness
validation. Format-1 absolute-root lockfiles migrate in memory to the portable
format-2 envelope; freshly compiled lockfiles never persist a checkout root.

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
| Hook output | **8 KiB per route** | Prevents verbose host output from consuming agent context. |
| OpenCode/Kilo plugin process output | **8 KiB combined stdout + stderr** | Concurrent drains prevent pipe deadlock; overflow, invalid UTF-8, timeout, and truncated decision JSON fail according to the registry route policy. |
| OpenCode/Kilo continuation state | **1,024 sessions / 10 accepted continuations each** | Bounded generation and in-flight state suppress duplicate idle delivery without storing prompts, tool payloads, or model output. |
| Compaction context | **4 KiB** | Restores control-plane orientation without replaying logs or task files. |
| Native assurance file | **4 MiB** | Rejects oversized source, manifest, or proof inputs before allocation. |
| Native assurance run | **4,096 files / 32 MiB reads** | Bounds aggregate source and evidence inspection across all gates. |
| Assurance findings | **50 + omitted-count marker** | Keeps policy output useful without consuming agent context. |

Breaches use the registry's platform-specific blocking response or exit code for
PreToolUse, permission, and Stop. Observation and cleanup routes fail open with
bounded warnings.

Bounded evidence uses raw, immutable segments instead of truncation. Each
segment carries the repository identity, session identity, policy-lock hash,
monotonic index, previous digest, and every evidence collection from the sealed
live epoch. Consumers replay the verified digest chain before evaluation, so
rotation changes storage shape rather than policy meaning. A triggering event
is retried only after the previous live epoch is durably sealed. Segment-chain
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
| MCP pre-action | **host capability** | Cursor's native pre-hook can deny an exact or strict-unclassified call. OpenCode/Kilo generic hooks enforce configured identities but cannot soundly identify unconfigured MCP calls. |
| MCP post-action | **fail-open, non-evidentiary on uncertainty** | Post-action blocking cannot undo a side effect. Positive evidence requires exact identity, valid selected values, and explicit success. |

The CLI applies the registry failure policy after handler execution as well as
during input decoding, so a handler cannot accidentally make an allow-route
blocking. Successful dispatch records per-route liveness outside the repository.
Each runtime route has a small six-hour marker: the common path is one `stat`,
zero locks, zero JSON reads, and zero writes; a due route refresh updates the
bounded aggregate status used by `reconc hook status`.

Cursor generation is registry-driven down to native event, matcher, runtime
route, response mode, failure policy, timeout, loop limit, and documented
surface. `postToolUse` is the authoritative successful generic signal;
`postToolUseFailure` is authoritative failure; `afterShellExecution` is
passive because Cursor provides no exit status there. Material signatures
deduplicate specialized write events against generic post-tool delivery.

OpenCode and Kilo plugins are generated transport adapters. Shell outcomes are
normalized from the exact integer `output.metadata.exit` into the neutral Go
payload. Their subprocess runner concurrently drains both pipes, applies one
combined output budget, validates UTF-8, and terminates the subprocess on
timeout. Idle continuation uses bounded per-session generations and only the
asynchronous SDK request `promptAsync({sessionID, messageID, parts})`. The
caller-owned message identifier distinguishes the injected callback from
external user activity; no synchronous prompt fallback exists.

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
evidence paths, and assurance inputs share `internal/pathidentity`; each caller
retains only its bounded purpose-specific containment policy.

### Command-injection

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

### Run state concurrency and Stop routing

`.reconc/run/state.bin` uses two CRC-protected slots and is published only for
material transitions.
Disabled hook events do not create run state or decisions. Enabled
continuations append one bounded decision even when the coarse run snapshot is
unchanged. Parallel agent tool calls can still spawn concurrent
`reconc hook runtime` processes, so three invariants keep an active run from
being silently disabled:

- **Crash-safe fixed layout.** Each state update writes a fixed 88-byte payload
  into the inactive 512-byte slot with a monotonic sequence and CRC32C over
  both header and payload. The
  payload stores timestamps as integers, the progress digest and canonical
  repository-root identity as 32 raw bytes each, and the disable reason as a
  bounded enum, so the hot read does not decode
  variable strings. Readers select the newest valid slot; a torn write leaves
  the previous slot intact and never decodes as disabled.
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
  taking a session lock, and each session owns its progress digest and six-stop
  counter, preventing deadlocks and cross-session budget interference.

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
optional committed completion reuses that terminal report's Git snapshot.
The shared `completiongate` used by Stop-facing views, `done`,
`post-task-check`, and TUI binds that snapshot to current policy, session
evidence, staged command proofs, and typed TASK completion. It captures state
again after evaluation and rejects races. A same-candidate explicit block is
stored outside the repository until a later validated explicit pass clears it;
retention cannot manufacture a pass.

`BenchmarkRepositoryRunStopHotpath` measures the in-process executable-TASK
continuation path without process startup. On Apple M1 its baseline was
1,504,653 ns/op, 61,612 B/op, and 553 allocs/op. Before durable continuation
records, the optimized median was 131,483 ns/op, 29,225-29,276 B/op, and 245
allocs/op. With one bounded decision-log record per continuation, the current
seven-run sample is 10,355,060-14,049,392 ns/op with a 10,986,170 ns/op median,
55,561-55,707 B/op, and 457 allocs/op. The routine path starts no Git process;
the current cost is the state and decision-log durability boundary. C/cgo would
not reduce those filesystem syscalls and would add a toolchain and portability
boundary, so the implementation remains pure Go.

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
- Audit and run-decision JSONL writes rotate before append through fixed archive
  rings; lifecycle retention bounds sessions, reports, locks, staged command
  proofs, the product-wide
  project-root set, generated binaries, and owned temp residue outside the Stop
  path.
- Native assurance source gates scan matching changed files only. Layout and
  substantive-proof authority gates inspect their complete configured surface;
  unreadable or over-budget authority fails closed.

### Secrets in audit

Tool-use `command` strings may contain API keys or tokens as
arguments. Default audit-log record only stores the FIRST token of
the command (e.g. `"go"` not `"go test -api-key=sk-..."`).
`RECONC_AUDIT_VERBOSE=1` opts into full command strings for debugging.

### Dependency review

reconc's non-stdlib dependencies processing the payload:
- `gopkg.in/yaml.v3` (YAML source parsing, not payload — irrelevant
  to this threat model).
- `github.com/bmatcuk/doublestar/v4` (glob matching — string-only
  surface, no eval).
- `mvdan.cc/sh/v3/syntax` (bounded AST parsing of untrusted shell text for
  command matching only; parsed input is never executed and unsupported or
  over-deep executable structure fails closed).
- `github.com/Microsoft/go-winio` plus `golang.org/x/sys/windows` (Windows-only
  named-pipe dialing and enumeration for Grok leader IPC; no network access,
  command execution, or JSON decoding).

No dep is used for JSON decoding; the stdlib `encoding/json` with
our own depth-limited decoder is the only entry point.

### What this threat model does NOT cover

- A compromised reconc binary itself (trust root).
- A deliberately hostile same-user process or agent replacing repository
  policy, hooks, state, or binaries; fabricating self-reported evidence;
  disabling host hooks; or bypassing Git hooks. External sandboxing and
  protected remote CI or branch rules must own that adversarial boundary.
- Kernel-level attacks (e.g. PID reuse allowing session-state
  tampering between runs). Out of scope; we assume the OS is sound.
- Network-borne attacks against Reconc's policy compiler/runtime (the core is
  offline and has no network surface). The explicit `reconc grok` command
  starts the external Grok ACP process; Grok owns its inference network,
  authentication, sandbox, and provider security boundaries. Leader steering
  uses only same-machine Unix sockets or Windows named pipes.
