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
- Compare RSS for the same fixed operation count, separately from timed
  throughput samples; retain the maximum and every existing tolerance.
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
- [x] Verify native source-bound execution and inspect every retained artifact and residual budget failure.
- [x] Separate RSS sampling with a fixed operation count; version its identity and preserve historical comparisons.
- [~] Verify the revised method locally and natively, reconcile implementation commits, archive, commit, and push.

## Notes

- Native `34614589075` completed recording and all five profile workloads for
  `f37fc6dbe432de64d751b1edb8ff9e7ae3d18a19`. All 30 profile artifacts passed
  size/SHA-256 verification; both clean source identities, all parameters, and
  every checked tolerance matched. CI `34614573128` passed all five jobs and
  CodeQL `34614573228` passed. The benchmark comparison failed only eleven
  RSS checks arising from three shared package-process measurements.
- The native action-cache binaries are byte-identical. Adjusted store time is
  2323 to 2156.818 ns/op, with unchanged 4608 B/op and three allocations.
  Ledger binaries are also byte-identical, yet their maximum RSS is 50.78 versus
  66.00 MiB; four candidate samples are around 29 MiB. Frame RSS is 84.08 versus
  192.42 MiB and the shared runtime process is 150.55 versus 183.42 MiB.
  These are retained failures, not proof of eleven distinct code regressions.
- A bounded local control in `.build/benchmarks/task520-memory-control/` runs
  precompiled binaries in alternating order, five samples and three internal
  repetitions. The time-based frame control reproduced one candidate peak of
  251.66 MiB among otherwise approximately 52 MiB samples. With exactly 64
  operations, baseline peaks are 51.42..53.31 MiB and candidate peaks are
  51.84..53.06 MiB. The identical ledger binary is likewise stable at fixed
  work (baseline 18.69..20.81 MiB, candidate 18.72..19.56 MiB).
- Implemented measurement contract: result/baseline v6 and comparison v8. Time and
  per-operation allocations keep the checked duration, sample count, and
  internal repetitions. After the closing CPU sentinel, run the same package
  patterns separately with `-test.benchtime=64x`, keeping repetitions/count.
  Retain their maximum RSS and explicit per-sample RSS operation count. Never
  replace the maximum with a median or weaken the unchanged RSS tolerance.
  Historical v4/v5 remain readable only within their own method; new baselines
  require actual v6 remeasurement of the exact source.
- Keep the existing package workload grouping and alternating sample order.
  Own one private temporary build directory per package run; compile its one or
  two source variants and the shared sentinel before entering the sample loop.
  Bound compilation, stdout, binary hashing, sampling, and cleanup explicitly.
- The initial result/baseline v5 contracts required a per-package executable SHA-256;
  historical v4 required its absence. Their comparison v7 exposed both identities
  and rejected mixed measurement formats. The workload suite and every numeric
  parameter/tolerance remained unchanged. A legacy reference could seed a v5 runner
  baseline only from an actual clean v5 measurement of its exact source.
- Fixed-work execution tests pass under the race detector. Real child-process
  tests separate timed metrics from a 32 MiB allocation exclusive to the RSS
  workload, preserve alternating source execution and binary reuse, and reject
  missing, duplicate, unmatched, canceled, or incorrectly labeled measurements.
  Go performs a one-operation warmup before each 64-operation repetition; the
  execution-order expectations explicitly include those warmups.
- Revised local gates passed: `make test-fast` (complete root and portable
  modules), `make vet`, `make lint`, and final scoped race/vet/staticcheck after
  error-message propagation. Logs use `/tmp/reconc-task520-fixed-rss-` prefixes;
  the final race suite completed in 10.216 seconds. Replaying native v5 evidence
  with comparison v8 preserves every group and all eleven RSS failures exactly.
  Native v6 recording and full CI remain required before completion.

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
