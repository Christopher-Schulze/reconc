# reconc -- Architecture

Short tour of how the pieces fit together, aimed at contributors and
anyone building on top of the library. For user-facing command
reference see `commands.md`.

## Pipeline

Every `reconc` invocation moves data through some subset of this pipe:

```
       repo root
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
```

`compile` stops at the lockfile. `check` / `ci` / `assert` / `can`
load the lockfile and run the runtime evaluator. `fix` / `explain`
also use the runtime then render the result. `why` / `diff` /
`watch` act purely on the lockfile.

## Package map

```
buildprovenance/ deterministic target/source identity + byte-only binary inspection
internal/
  adopt/          convention detector (JS/TS, Python, Rust, Go, CI, dirs)
  agentguide/     embedded agent-integration guide + section lookup
  assurance/      bounded native layout/source/manifest/proof gates
  atomicfile/     write-on-change and atomic publication primitives
  audit/          append-only JSONL decision log + rotation + stats
  bootstrap/      deterministic new/mature-repo transactions + binary resolution
  changelog/      docs/changelog.md rotation into quarterly archives
  cli/            command dispatch plus responsibility-owned command modules
  compiler/       lockfile builder: digest, writer, conflicts, migrations, lock
  completion/     bash / zsh / fish completion generators
  contextsize/    token-budget guard for canonical entrypoints + active TASK
  errors/         typed exception hierarchy (PolicySourceError, LockfileError, ...)
  extractor/      prose-to-rule heuristic scanner (regex-only, no LLM)
  grokacp/        strict Grok ACP stdio client + cross-platform leader IPC stop steering/probing
  hooks/          typed platform registry + generators + installers + activation probes + scaffold sync
  ingest/         discovery + source loading (AGENTS.md, .reconc.yml, presets, globals)
  lockdiff/       structural lockfile comparison (ignore-provenance semantics)
  filelock/       cross-platform process locks
  jsonl/          bounded locked JSONL append + archive rings
  parser/         YAML-to-Rule validation + template expansion + scope expansion
  policy/         Rule / Scope / Source / Kind / Mode types
  presets/        bundled policy packs (embed.FS) + user overlays
  runtime/        evaluator + remediation + git integration + subprocess runner
  retention/      bounded runtime storage lifecycle + owned temp cleanup
  runtime/agentsession/  hook payload handlers, session evidence state,
                  stop policy cache, run-state store (the package the
                  hook-runtime threat model below describes)
  scaffold/       reconc init implementation
  schema/         canonical public JSON contract URLs + enterprise override
  tasklifecycle/  typed TASK profiles + recoverable state transactions
  templates/      bundled rule-shape templates (embed.FS) + user overlays
  tui/            dependency-free terminal dashboard
```

`cmd/reconc/main.go` is ~20 lines: parse argv, delegate to
`cli.Run`, translate the returned error into an exit code.
Within `internal/cli`, the 212-line `cli.go` owns only public errors, top-level
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
   transaction. Bootstrap is create-only, emits candidate files for drift, and
   rolls back only transaction-owned unchanged files. Hook merges preserve
   unrelated host configuration. Bounded JSONL writers rotate under a process
   lock before append.

4. **Explicit side effects.** Compile, bootstrap, hook installation, TASK
   mutation, retention, and hook event handling own their documented files.
   Read-only commands never refresh policy. `RECONC_AUDIT=1` is still required
   for the optional decision audit log.

5. **Advisory compile lock.** `.reconc/.compile.lock` via O_EXCL
   prevents two `reconc compile` from racing. 60s stale-reap so a
   crashed compile doesn't wedge the repo forever.

6. **Satisfiable conflict analysis.** Static command contradictions follow
   runtime `require_command` semantics: any configured alternative can satisfy
   the rule. A forbid/require pair is reported only when their exact trigger
   scopes overlap and one forbid rule blocks every required alternative.

## Key external contracts

- **Lockfile schema** (`$schema` in policy.lock.json): bumped only on
  shape-breaking changes. Migration chain in `compiler/migrations.go`
  walks older versions forward.

- **CheckReport / FixPlan schemas**: same policy. Additive changes
  (new optional fields) don't bump the version; breaking changes do.

- **Published schema documents**: `schemas/v1/*.schema.json` are the canonical
  Draft 2020-12 contracts, use format-versioned repository URLs as `$id`, and
  ship in the checksummed release inventory. `policy-config.schema.json` is the
  strict authoring contract; lock, report, and fix-plan schemas describe emitted artifacts.

- **Exit codes 0/1/2**: stable across all subcommands for agent
  consumption. 0 = pass or warn, 1 = runtime/input error, 2 = at
  least one blocking violation.

- **Env vars** (`RECONC_HOME`, `RECONC_AUDIT`, `RECONC_SCHEMA_BASE_URL`):
  stable names. Adding a new one is additive; renaming or removing
  needs a major version bump.

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
3. Add an entry to the `printUsage` help text in the correct category.
4. Add the subcommand to `completion.Subcommands` in
   `internal/completion/completion.go` so shell completion stays in
   sync.
5. Write tests in `internal/cli/cli_test.go`: happy path + at least
   one error path + `--help`.
6. Document in `docs/commands.md` under the right category.

The typical commit diff for a new subcommand touches the dispatcher, one
responsibility-owned command file, completion metadata, focused tests, and
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
  cli ──┬──► compiler ──► parser ──► ingest
        │       ▲
        │       └── migrations, conflicts, lock
        │
        ├──► runtime ──┬──► policy
        │              ├──► assurance ──► policy
        │              └── template substitution, script runner, git
        │
        ├──► hooks
        ├──► adopt
        ├──► extractor
        ├──► lockdiff
        ├──► audit
        ├──► changelog
        ├──► contextsize
        ├──► agentguide (embed)
        ├──► templates  (embed)
        ├──► presets    (embed)
        ├──► scaffold
        └──► completion
```

Nothing below `cli` imports `cli`. The compiler does not know about the runtime;
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
| Max payload bytes | **1 MiB** (1 048 576) | No legitimate tool-use payload exceeds ~100 KiB; 1 MiB leaves 10x headroom + stops JSON bombs. |
| stdin read timeout | **5 seconds** | Prevents agent hangs from wedging the hook call. Typical payloads arrive < 50 ms. |
| Max JSON nesting depth | **32 levels** | Prevents stack-busting via deeply nested payloads. |
| Max persisted session state | **1 MiB** | Bounds full-file state publication and recovery cost. |
| Evidence collections | **item + byte caps per field** | Overflow is persisted and fails closed; relevant evidence is never silently omitted. |
| Audit record | **32 KiB** | Bounds one locked JSONL append. |
| Audit/run storage | **2 MiB live + 2 archives each** | Fixed rings and transition-only run records prevent repository-local log growth. |
| Hook output | **8 KiB per route** | Prevents verbose host output from consuming agent context. |
| Compaction context | **4 KiB** | Restores control-plane orientation without replaying logs or task files. |
| Native assurance file | **4 MiB** | Rejects oversized source, manifest, or proof inputs before allocation. |
| Native assurance run | **4,096 files / 32 MiB reads** | Bounds aggregate source and evidence inspection across all gates. |
| Assurance findings | **50 + omitted-count marker** | Keeps policy output useful without consuming agent context. |

Breaches use the registry's platform-specific blocking response or exit code for
PreToolUse, permission, and Stop. Observation and cleanup routes fail open with
bounded warnings.

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

The CLI applies the registry failure policy after handler execution as well as
during input decoding, so a handler cannot accidentally make an allow-route
blocking. Successful dispatch records per-route liveness outside the repository.
Each runtime route has a small six-hour marker: the common path is one `stat`,
zero locks, zero JSON reads, and zero writes; a due route refresh updates the
bounded aggregate status used by `reconc hook status`.

### Path-traversal

Every path in the payload is:
1. Joined with the project root.
2. `filepath.Clean`'d.
3. `filepath.EvalSymlinks`'d where the path exists.
4. Tested for containment in the (symlink-resolved) project root.
5. Rejected with `RepoBoundaryError` if outside.

Implemented in the agentsession handlers (`filepath.EvalSymlinks`
containment in `handlers.go` and `state.go`) and mirrored by the
evaluator's `normalizePaths`/`sameCanonicalPath`; the assurance file
walker uses its own `canonicalRoot`.

### Command-injection

Command / tool-use strings in the payload are **data**, never
executed by reconc. The evaluator's rule-matching compares them as
strings; no `exec.Command` call path in the runtime handlers takes
user data as the binary name or unescaped argument. Enforced by a
source-scan guard test (`TestNoExecCommandTakesNonLiteralBinary` in
`internal/runtime/agentsession`): every exec.Command binary in the
payload-handling package must be a string literal.

### Session identity boundary

The host runtime supplies `session_id`; Reconc sanitizes it for the filename and
stores evidence in that session-specific file under the canonical repo hash.
Different IDs therefore cannot merge accidentally. Reconc does not generate,
authenticate, HMAC, or expire host session IDs, so a hostile host process with
the same user/filesystem authority remains outside this boundary. Malformed
state fails closed instead of being reset silently.

### Run state concurrency and Stop routing

`.reconc/run/state.bin` uses two CRC-protected slots and is published only for
material transitions.
Disabled and unchanged hook events do not create or rewrite state or
decisions. Parallel agent tool calls can still spawn concurrent
`reconc hook runtime` processes, so two invariants keep an active run from
being silently disabled:

- **Crash-safe fixed layout.** Each state update writes a fixed 56-byte payload
  into the inactive 512-byte slot with a monotonic sequence and CRC32C over
  both header and payload. The
  payload stores timestamps as integers, the progress digest as 32 raw bytes,
  and the disable reason as a bounded enum, so the hot read does not decode
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

Repository mode is controlled only by `reconc run on|off`. Prompt text,
runtime interrupts, session lifecycle events, runtime changes, compaction, and
application restarts never mutate its locked state. An explicit interrupt
releases only the current host invocation. `run off` is the only manual
disable action; complete or absent TASK state disables it automatically after
the terminal gates.

Stop reads TASK state through `tasklifecycle.InspectRunState`. Executable
`continue` and `claim` dispositions return the runtime-native continuation
response before policy report construction and without a Git process.
`blocked`, `complete`, and `absent` dispositions continue to the terminal Stop
gate; `invalid` fails closed. This fast path never bypasses PreToolUse, TASK
mutation transactions, pre-commit, or terminal policy enforcement. The
no-progress guard compares typed TASK state plus a write/command material-event
counter; reads and unrelated events cannot fake progress. At the limit,
repository mode releases one Stop and resets the guard while leaving the
explicit durable switch enabled. Repository runs leave the fast
path only after 64 new material events, 30 minutes with new progress, or a
failed command, then reuse the normal full Stop report as a policy checkpoint.
Explicitly configured TASK state fails closed if its overview disappears, and
optional committed completion reuses that terminal report's Git snapshot.

`BenchmarkRepositoryRunStopHotpath` measures the in-process executable-TASK
continuation path without process startup. On Apple M1 its baseline was
1,504,653 ns/op, 61,612 B/op, and 553 allocs/op. The optimized seven-run sample
is 130,819-142,849 ns/op with a 131,483 ns/op median, 29,225-29,276 B/op, and
245 allocs/op. The routine path starts no Git process and publishes no decision
log record. C/cgo would not reduce the dominant filesystem syscalls and would
add a toolchain and portability boundary, so the implementation remains pure
Go.

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
normalized matching.

The RTK-prefix strip in `normalizeCommandSemantics` is a compatibility
shim for transparent CLI proxies: a command recorded as `rtk go test
./...` satisfies a rule authored as `go test ./...` because the proxy
preserves the wrapped command's semantics and exit status. It is not a
coupling to any specific tool beyond recognizing that prefix.

### Resource exhaustion

- `ResourceLimitedJSONReader` wraps stdin: bails at 1 MiB + 5s
  timeout + 32-level depth.
- Session evidence has per-field item and byte caps plus a 1 MiB serialized
  ceiling. Overflow persists a fail-closed marker used by PreToolUse and Stop.
- Audit and run-decision JSONL writes rotate before append through fixed archive
  rings; lifecycle retention bounds sessions, reports, locks, the product-wide
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
