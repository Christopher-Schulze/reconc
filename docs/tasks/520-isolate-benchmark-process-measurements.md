# TASK 520: Isolate benchmark process measurements

## Why

TASK 519 completes the compiler and approved comparison changes, but native
run 34608558526 retains absolute cache timing and process RSS failures. The
previous recorder measured the Go driver and rebuilt its sentinel around each sample.
Compilation and source-path-sensitive binaries must not contaminate measured
workloads or be presented as isolated application memory regressions.

## Acceptance

- Compile each package binary and its CPU sentinel before measured repetitions;
  execute the binaries directly with the existing sample counts and durations.
- Measure RSS from the benchmark process, retain deterministic executable
  SHA-256 identities, and preserve alternating baseline/candidate execution.
- Distinguish precompiled measurements from historical Go-driver measurements;
  reject mixed comparisons while retaining read-only historical validation.
- Keep the checked baseline bytes and every reviewed tolerance unchanged;
  rebuild its exact source for compatible runner-local comparisons.
- Verify real execution, cancellation, missing benchmarks, input preservation,
  format compatibility, and source/executable identity with local/native gates.
  Diagnose remaining cache observations without silently changing budgets.

## Sub-Tasks

- [x] Inspect recorder, parser, contracts, baseline binding, and all callers; define the bounded binary lifecycle and compatibility rules.
- [x] Precompile reproducible binaries and sentinel, directly sample processes, and retain executable hashes without weakening output/time/resource bounds.
- [x] Version measurement contracts, preserve legacy reference validation, and propagate the exact method into comparison and documentation.
- [x] Verify real subprocess behavior and local gates; commit and push the immutable source required by native recording.
- [~] Verify native source-bound evidence, reconcile the implementation commit, archive, commit, and push.

## Notes

- Keep the existing package workload grouping and alternating sample order.
  Own one private temporary build directory per package run; compile its one or
  two source variants and the shared sentinel before entering the sample loop.
  Bound compilation, stdout, binary hashing, sampling, and cleanup explicitly.
- New result/baseline v5 contracts require a per-package executable SHA-256;
  historical v4 requires its absence. Comparison v7 exposes both identities
  and rejects mixed measurement formats. The workload suite and every numeric
  parameter/tolerance remain unchanged. A legacy reference can seed a v5 runner
  baseline only from an actual clean v5 measurement of its exact source.

- Baseline source remains `c814f5b40579d2feed47231ec9dc2d80afda1dea` and checked
  file SHA-256 `dbf4c975b819b539447376cb0906c4c99bf93093d0540ec6965b2702cb1b4dde`.
- The initial `runBenchmarkSample` and `runCPUSentinelSample` ran `go test` and
  collected its `ProcessState`. The old `runPairedPackageBenchmarks` alternated
  roots, but each call rebuilt the sentinel in a fresh temporary directory.
- Precompile with `go test -c -trimpath` except for `./internal/cli`, whose
  unchanged historical transport workload requires real `runtime.Caller`
  source paths and builds a child CLI during setup. Its hash remains path
  sensitive; do not claim identical-source reproducibility for that package.
  Use direct `-test.*` flags and the existing bounded executor/parser metric
  validation. Package-process RSS is
  explicitly distinct from per-operation allocation and retained-cache budgets.
- Record binary hashes in new measurement contracts. Legacy results remain
  valid only within their own method; reference source/tolerances can produce a
  new runner baseline after actual remeasurement. Never relabel old RSS values.
- Native CI/code correctness is already green for 3bdd015e. No current production
  action-cache source change is proven; local action machine instructions are
  identical. The persistent native timing observation needs measurement controls.
- Real direct-execution smoke recordings completed all 19 groups and 24 targets
  for both current work and the clean exact checked source. These one-iteration
  recordings prove execution and method compatibility, not timing improvements.
  The action-package binaries are byte-identical across roots/source commits:
  `62337a7acf19491aba3d62d0d636a555c80b65845507cd3672ec98f0bd402ef5`.
- Real subprocess tests verify alternating source execution, collapsed internal
  repetitions, source-independent binary identity, working-directory-sensitive
  file reads, measured allocation/RSS, cancellation before and during execution,
  and refusal to recompile between samples even when new source files make
  compilation fail. Historical
  comparisons remain valid; mixed methods, malformed/inconsistent hashes, old
  measurements relabeled as new baselines, and hash-based budget bypasses fail.
- Local verification passed: `make test` (publication/reference/harness audits,
  uncached root and portable-template race suites, real release trust),
  `make vet`, `make lint`, and `git diff --check`. The final scoped
  `go test -race ./scripts/benchmarks/history -count=1` also passed after the
  binary-file preflight and live-cancellation coverage were added; scoped vet
  and staticcheck passed. Logs: `/tmp/reconc-task520-test.log`,
  `/tmp/reconc-task520-vet.log`, `/tmp/reconc-task520-lint.log`.

## Deviations

- Source-bound native recording requires a clean published commit before its
  evidence exists. Commit the implementation after local gates, keep this task
  active during native verification, then archive its verified outcome in the
  final documentation commit. This avoids claiming native success in advance.
