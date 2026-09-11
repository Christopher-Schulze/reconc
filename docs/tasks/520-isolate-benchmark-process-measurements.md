# TASK 520: Isolate benchmark process measurements

## Why

TASK 519 completes the compiler and approved comparison changes, but native
run 34608558526 retains absolute cache timing and process RSS failures. The
recorder measures the Go driver and rebuilds its sentinel around each sample.
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

- [ ] Inspect recorder, parser, contracts, baseline binding, and all callers; define the bounded binary lifecycle and compatibility rules.
- [ ] Precompile reproducible binaries and sentinel, directly sample processes, and retain executable hashes without weakening output/time/resource bounds.
- [ ] Version measurement contracts, preserve legacy reference validation, and propagate the exact method into comparison and documentation.
- [ ] Verify real subprocess behavior and native source-bound evidence; archive, commit, and push.

## Notes

- Baseline source remains `c814f5b40579d2feed47231ec9dc2d80afda1dea` and checked
  file SHA-256 `dbf4c975b819b539447376cb0906c4c99bf93093d0540ec6965b2702cb1b4dde`.
- `runBenchmarkSample` and `runCPUSentinelSample` currently run `go test` and
  collect its `ProcessState`. `runPairedPackageBenchmarks` alternates the roots,
  but each call rebuilds the sentinel in a fresh temporary directory.
- Precompile with `go test -c -trimpath`; use direct `-test.*` flags and the
  existing bounded executor/parser metric validation. Package-process RSS is
  explicitly distinct from per-operation allocation and retained-cache budgets.
- Record binary hashes in new measurement contracts. Legacy results remain
  valid only within their own method; reference source/tolerances can produce a
  new runner baseline after actual remeasurement. Never relabel old RSS values.
- Native CI/code correctness is already green for 3bdd015e. No current production
  action-cache source change is proven; local action machine instructions are
  identical. The persistent native timing observation needs measurement controls.

## Deviations

None.
