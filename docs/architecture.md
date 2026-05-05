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
internal/
  adopt/          convention detector (JS/TS, Python, Rust, Go, CI, dirs)
  agentguide/     embedded agent-integration guide + section lookup
  audit/          append-only JSONL decision log + rotation + stats
  changelog/      docs/changelog.md rotation into quarterly archives
  cli/            argparse-equivalent dispatcher (one file, big switch)
  compiler/       lockfile builder: digest, writer, conflicts, migrations, lock
  completion/     bash / zsh / fish completion generators
  contextsize/    token-budget guard for auto-loaded session files
  errors/         typed exception hierarchy (PolicySourceError, LockfileError, ...)
  extractor/      prose-to-rule heuristic scanner (regex-only, no LLM)
  hooks/          git + claude-code + codex generators / installers
  ingest/         discovery + source loading (AGENTS.md, .reconc.yml, presets, globals)
  lockdiff/       structural lockfile comparison (ignore-provenance semantics)
  parser/         YAML-to-Rule validation + template expansion + scope expansion
  policy/         Rule / Scope / Source / Kind / Mode types
  presets/        bundled policy packs (embed.FS) + user overlays
  runtime/        evaluator + remediation + git integration + subprocess runner
  scaffold/       reconc init implementation
  templates/      bundled rule-shape templates (embed.FS) + user overlays
```

`cmd/reconc/main.go` is ~20 lines: parse argv, delegate to
`cli.Run`, translate the returned error into an exit code.

## Key invariants

1. **Byte-stable lockfile.** Two compiles of identical sources produce
   identical bytes. Compiler emits canonical JSON (sorted keys,
   indent-2, trailing newline). Source digest is SHA-256 over the
   same canonical form. Enables rsync-style drift detection and
   git-friendly diffs.

2. **Fail-closed on tampering.** Unknown rule kind, malformed YAML,
   stale lockfile, mismatched repo_root, unsupported schema URL --
   every degradation path raises a typed error rather than silently
   treating the situation as "pass".

3. **Idempotent writes.** `compile`, `init`, `bootstrap`, `hook
   install claude-code|codex`, `adopt --apply`, `changelog rotate`,
   `audit` append -- every write path can be re-run without
   duplicating state. Where the target file pre-exists, reconc-owned
   entries are replaced; user-owned entries are preserved.

4. **Opt-in side effects.** `RECONC_AUDIT=1` enables the decision
   log. With no env override reconc leaves no files behind outside
   `.reconc/policy.lock.json` (the one file that is its job to
   produce).

5. **Advisory compile lock.** `.reconc/.compile.lock` via O_EXCL
   prevents two `reconc compile` from racing. 60s stale-reap so a
   crashed compile doesn't wedge the repo forever.

## Key external contracts

- **Lockfile schema** (`$schema` in policy.lock.json): bumped only on
  shape-breaking changes. Migration chain in `compiler/migrations.go`
  walks older versions forward.

- **CheckReport / FixPlan schemas**: same policy. Additive changes
  (new optional fields) don't bump the version; breaking changes do.

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
   - `loadLockfile(root)` reads + validates schema / version /
     repo_root.
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

1. Write `runFoo(args []string, stdout, stderr io.Writer) error` in
   `internal/cli/cli.go`. Use `CLIError{ExitCode, Message}` for
   typed failures.
2. Add a `case "foo": return runFoo(argv[1:], ...)` to the dispatcher
   switch.
3. Add an entry to the `printUsage` help text in the correct category.
4. Add the subcommand to `completion.Subcommands` in
   `internal/completion/completion.go` so shell completion stays in
   sync.
5. Write tests in `internal/cli/cli_test.go`: happy path + at least
   one error path + `--help`.
6. Document in `docs/commands.md` under the right category.

The typical commit diff for a new subcommand touches: cli.go,
completion.go, cli_test.go, and commands.md. ~80-150 LOC including tests.

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
        ├──► runtime ──► policy
        │       └── template substitution, script runner, git
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

Nothing below `cli` imports `cli`. The compiler doesn't know about
the runtime (the lockfile is the boundary). The runtime only imports
`compiler` for the shared schema constants.

## Threat model: hook runtime

`reconc hook runtime <event>` accepts a JSON payload on stdin from
the agent process (Claude Code, Codex). That payload is **untrusted
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
| Max audit entries per session | **10 000** | Caps log growth under a runaway hook loop. |
| Session lifetime | **24 hours** | Stale session state is discarded. |

Breaches: exit 2 (block) for PreToolUse / Stop; exit 0 (allow)
with stderr warning for PostToolUse / SessionEnd.

### Fail-closed vs fail-open

Decision is per-event based on the security role of the event:

| Event | Malformed payload | Reasoning |
|---|---|---|
| `SessionStart` | **fail-closed** (exit 2) | Session can't be trusted without a valid start. |
| `PreToolUse` | **fail-closed** (exit 2) | This event GATES a write/command; uncertain input must not allow. |
| `PostToolUse` | **fail-open** (exit 0, stderr warn) | Observation-only; blocking here doesn't prevent already-done damage and just disrupts the session. |
| `PostToolUseFailure` | **fail-open** (exit 0, stderr warn) | Same as PostToolUse. |
| `Stop` | **fail-closed** (exit 2) | GATES session completion; uncertain input must block. |
| `SessionEnd` | **fail-open** (exit 0, stderr warn) | Cleanup-only; forced close shouldn't propagate errors. |

### Path-traversal

Every path in the payload is:
1. Joined with the project root.
2. `filepath.Clean`'d.
3. `filepath.EvalSymlinks`'d where the path exists.
4. Tested for containment in the (symlink-resolved) project root.
5. Rejected with `RepoBoundaryError` if outside.

Shared helpers: `canonicalRoot` + `canonicalPath`.

### Command-injection

Command / tool-use strings in the payload are **data**, never
executed by reconc. The evaluator's rule-matching compares them as
strings; no `exec.Command` call path in the runtime handlers takes
user data as the binary name or unescaped argument. Verified by a
grep-guard test.

### Replay-attack mitigation

- Each session generates a fresh UUID-like `session_id` at
  `SessionStart`.
- All subsequent events are required to carry the same session_id;
  mismatches are rejected with exit 2.
- Session state records `started_at` (RFC3339 UTC) and rejects
  events with `session_id` older than the 24h lifetime.
- Session-state file is HMAC-tagged (lockfile_digest + session_id +
  started_at) so manual tampering is detected and causes reconc to
  discard the state and start a fresh session.

### Resource exhaustion

- `ResourceLimitedJSONReader` wraps stdin: bails at 1 MiB + 5s
  timeout + 32-level depth.
- Session state's `evidence` slices cap at 10 000 entries each;
  further events surface a WARN in audit but do not append.
- Audit-log rotation prevents disk-fill DoS.

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

No dep is used for JSON decoding; the stdlib `encoding/json` with
our own depth-limited decoder is the only entry point.

### What this threat model does NOT cover

- A compromised reconc binary itself (trust root).
- Kernel-level attacks (e.g. PID reuse allowing session-state
  tampering between runs). Out of scope; we assume the OS is sound.
- Network-borne attacks (reconc is offline; no network surface).
