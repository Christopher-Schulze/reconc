# TASK 183: Precompile runtime path matchers

## Why

`internal/runtime/match.go` calls `doublestar.Match` for each pattern/path
comparison. Runtime checks repeatedly evaluate the same validated patterns, so
the library reparses pattern syntax on the hot path. The installed doublestar
version has no `Compile` API; the earlier session's proposed direct call was
invented and cannot be used.

## Acceptance

- Runtime plans contain an immutable, reusable representation for every path
  pattern, created only after the existing grammar and bound checks pass.
- Matching preserves doublestar behavior exactly for separators, `**`,
  character classes, escaping, invalid patterns, and platform normalization.
- No unvalidated pattern reaches an unchecked matching API.
- Benchmarks cover matching rule counts and path counts representative of hook
  evaluation and demonstrate the improvement without increasing plan size
  beyond explicit bounds.
- Compiler/runtime compatibility and lockfile format behavior remain explicit.

## Sub-Tasks

- [x] Evaluate supported internal matcher representations against doublestar
- [x] Define compiled matcher ownership in the runtime plan
- [x] Replace repeated parsing while preserving validation
- [x] Add differential, fuzz, and benchmark tests
- [x] Run runtime and complete Go gates

## Notes

- Root evidence: `MatchPath` and `MatchAny` in
  `internal/runtime/match.go`.
- `github.com/bmatcuk/doublestar/v4` v4.10.0 exposes validation and
  `MatchUnvalidated`, but no compile API. The runtime now reuses the existing
  immutable `internal/action.CompiledGlob` token program for bounded patterns,
  with a validated `MatchUnvalidated` fallback for patterns outside the
  explicit 1 MiB per-matcher and 4 MiB aggregate admission budget. This keeps
  the lockfile grammar and wire format unchanged while bounding added plan
  memory.
- `runtimePlan.pathMatchers` owns one deterministic map of every top-level and
  nested runtime path pattern (`paths`, `before_paths`, `when_paths`,
  `scope_paths`). Evaluation contexts use the plan-owned map for scope,
  trigger, deny-write, read, coupling, claim, command, assurance, and
  composite deny-write checks. The public compatibility helpers still validate
  dynamic caller-supplied patterns through `MatchPath`.
- The matcher differential corpus covers literals, `*`, `**`, alternatives,
  classes, escaping, whitespace normalization, malformed patterns, and a
  fuzz parity harness. The representative 64-comparison benchmark measured
  approximately 1.6 microseconds and zero allocations for compiled programs
  versus approximately 3.2 microseconds for reparsing with `MatchPath` on the
  local Apple M1 run.
- Verification: `go test ./internal/runtime ./internal/action`,
  `go test -race ./internal/runtime`, and full `make test` all passed. The full
  gate included format checking, publication audit, harness-pack validation,
  race-enabled `./...`, harness template races, and release trust.

## Deviations

None.
