# TASK 269: Eliminate repeated doctor and hook inspection work

## Why

One `doctor --deep` run independently validates lock freshness, reloads and
parses the policy corpus for conflicts, and rereads overlapping files for
preset/template references. Hook status also generates artifacts more than
once and rereads files it already holds, while cross-runtime deduplication may
perform the same filesystem inspection per event. These are user-visible
latency costs in commands expected to be fast diagnostics.

## Acceptance

- `doctor --deep` loads one bounded, identity-checked policy source bundle and
  derives freshness, parsed rules, conflicts, and preset/template references
  from shared immutable results where their trust boundary is identical.
- Checks remain independently reportable: failure to derive one result does
  not fabricate success for another, and all existing check names, severities,
  JSON fields, source limits, and error context remain stable.
- Hook status generates each platform artifact at most once per inspection and
  validates the bytes already read instead of reopening the same target without
  a required identity revalidation reason.
- Cross-runtime deduplication memoizes first-class platform readiness within one
  hook event/request only. It never reuses readiness across repository roots or
  after a mutation boundary.
- Custom-runtime status obtains manifest digest/freshness data without building
  a full evaluator solely for a digest lookup, while retaining the exact
  compiled-lock trust chain.
- The Go hook worker frame reader avoids geometric growth for normal maximum
  frames without preallocating unbounded memory; capacity is derived from the
  enforced frame limit and benchmark evidence.
- Before/after benchmarks cover deep doctor, multi-platform status, repeated
  dedup checks, and worker transport. Allocation claims are recorded in the
  calibrated benchmark workflow where stable.
- CLI output tests, hook tests, race tests, docs, scripts, and complete gates
  pass.

## Sub-Tasks

- [x] Introduce one immutable deep-doctor analysis context from the existing bounded source loader
- [x] Rewire freshness, conflict, and reference checks to consume shared results without coupling failures
- [x] Generate and read each hook artifact once per status inspection
- [x] Add request-scoped platform-inspection memoization for route deduplication
- [x] Expose the minimum trusted custom-runtime digest lookup surface
- [x] Right-size the Go worker frame buffer from measured frame distributions and hard limits
- [x] Add focused benchmarks and allocation regression tests
- [x] Update diagnostic and performance documentation plus benchmark history
- [x] Run CLI, hook, runtime, race, and complete repository verification

## Notes

- Current evidence: `buildDoctorDeepReport` invokes independent checks;
  `doctorCheckLockfileFreshness`, `doctorCheckConflictCount`, and
  `collectDoctorRefs` each trigger overlapping source work.
- Current evidence: `finalizePlatformStatus` calls `Generate`, while
  `inspectPlatform` has already called it; status paths also read targets again
  to compute installation state.
- Current evidence: `dedupToFirstClassRoute` calls `hooks.InspectPlatform` for
  every event.
- This task must not cache filesystem observations across command invocations or
  weaken stable-file identity checks. It shares work inside one bounded command
  execution only.
- Removing thin dependency-injection wrappers is not an objective. Refactor
  them only if the shared context cannot otherwise be injected cleanly.
- Apple M1 repeated measurements reduced deep-doctor bytes/op by about 10.8%
  and allocations/op by about 16.3%; multi-platform hook-status bytes/op by
  about 30.8% and allocations/op by about 14.8%.
- A 1 MiB worker frame moved from about 4.76 MiB and 13 allocations to about
  1.39 MiB and 4 allocations. The calibrated history now tracks the 64 KiB
  representative frame and 1 MiB target.
- Verification passed: focused and complete package tests, selected race tests,
  Windows cross-compilation, calibrated benchmark comparison, `make
  test-fast`, vet, Staticcheck, publication audit, self-hosting, and the full
  release-trust harness.

## Deviations

None.
