# TASK 519: Resolve residual benchmark memory and hook timing

## Why

TASK 518 establishes compatible paired source measurements while preserving all
reviewed budgets. Run 34597552388 retains two compiler RSS violations on one
package process and one warm-prefix normalized timing violation. The failures
require attributable diagnosis and a bounded fix, not new tolerances or a
replacement source baseline.

## Acceptance

- Reduce unnecessary read-only compiler conflict-index retention without changing
  conflict pairs, ordering, output limits, path ownership, or exception semantics.
- Compare unchanged and corrected compiler workloads using real precompiled
  binaries, complete samples, peak RSS, and allocation measurements.
- Diagnose the warm-prefix ratio using retained raw samples, source changes, and
  profiles; preserve all path, evidence, freshness, and cancellation guarantees.
- Keep the checked baseline and all limits unchanged. Required local/native gates
  and a paired run verify the corrected source; disclose any residual violation.

## Sub-Tasks

- [x] Inspect conflict-index ownership and every caller; measure the exact unchanged workload.
- [x] Remove proven redundant rule copies and verify output parity and input/output isolation.
- [x] Complete bounded warm-prefix attribution with preserved raw/profile evidence.
- [x] Apply the approved target-and-ratio comparison rule, verify unchanged absolute limits, and investigate the independent cache timing observation.
- [x] Run local/native gates, inspect the full comparison, and split the remaining measurement isolation into TASK 520.

## Notes

- Candidate `3bdd015e190d1b670b06050e496da3458b6e3b82` is committed and
  pushed. Native CI 34608550555 passes all five jobs; CodeQL 34608550990 passes.
  Benchmark 34608558526 completes recording/profiling and artifact retention,
  then fails comparison with ten absolute observations: cache-store time
  2,077 to 2,695.699 CPU-adjusted ns; frame-process RSS 354,369,536 to
  469,417,984 bytes; eight runtime targets share 227,835,904 to 282,984,448 bytes.
  All source/parameter bindings, unchanged tolerances, 19 groups, 24 targets,
  and 30 profile hashes/lengths are verified in
  `.build/benchmarks/task519-ci-34608558526/`. No normalized metric blocks.
- Source inspection confirms `runBenchmarkSample` records `ProcessState` from
  `go test`, and the CPU sentinel is rebuilt before and after every package
  sample. This measures the Go driver/build process and inserts compilation
  between alternating source measurements. TASK 520 owns direct precompiled
  process measurement and executable-identity controls. The approved comparison
  and compiler changes are complete; the overall performance gate remains open.

- The approved comparison change passes the complete uncached root/template
  race suites, publication/reference checks, vet, staticcheck, and release-trust
  (real isolated release target: 90s). Ten targeted boundary/drift scenarios and
  the recipe CLI regression also pass. Local log prefix:
  `/tmp/reconc-task519-approved-`. Native verification is recorded above.
- Replaying the retained native inputs with comparison v6 leaves every group
  metric exactly unchanged. Blocking violations decrease from four to only the
  absolute cache-store observation; all three ratio exceedances remain visible.
  `.build/benchmarks/task519-ci-34601720834/comparison-v6.json` preserves this
  result alongside the original v5 report. The checked baseline SHA-256 remains
  `dbf4c975b819b539447376cb0906c4c99bf93093d0540ec6965b2702cb1b4dde`.
- All 143,304 action-package machine instructions in the preserved local
  baseline/current test binaries are identical (SHA-256
  `a1e13f675ede6ff3a59a9f423c6fc57aa6f18ca1a8faf25982383a393b5aa729`).
  The package depends only on unchanged external modules and the standard
  library. `.build/benchmarks/task519-action/machine-code-comparison.json`
  retains the comparison and both disassemblies. This is local code-identity
  evidence; it does not claim inspection of the native CI binaries or establish
  the external cause of the historical timing fluctuation.

- Christopher approved the target-and-ratio rule: a normalized exceedance blocks
  only when the target also degrades beyond that normalized metric's tolerance.
  Time uses the existing CPU-adjusted target measurement. Preserve all absolute
  limits, raw observations, and ratio exceedances; version only the comparison
  contract for the changed blocking meaning. Boundary, CPU drift, resource,
  zero-baseline, and recipe-propagation regressions pass.

- Candidate 2f8ae5b256eee62e28b0bfe6b18185dfcd6f8a95 is committed and pushed.
  Native CI 34601707654 passes all five jobs; CodeQL 34601707560 passes.
  Native benchmark 34601720834 records compatible exact clean sources and
  retains all 19 groups, 24 targets, and 30 hash/length-verified profile files
  in `.build/benchmarks/task519-ci-34601720834/`. Reference parameters and all
  tolerances are unchanged; comparison remains failed and is not hidden.
- This run reports normalized root-predicate time +30.24%, normalized compiler
  duplicate time +132.01%, normalized compiler duplicate B/op +614.16%, and
  absolute CPU-adjusted cache-store time +23.20%. Compiler target raw time
  improves while its calibration improves more; target B/op changes only
  +0.0795%. Compiler process RSS is 372,391,936 baseline versus 164,659,200
  candidate bytes. The same baseline source had a much lower process peak in
  the prior run, so this Go-driver high-water mark is not isolated workload
  attribution. The separately measured direct-binary improvement remains valid.
- `internal/action` has no source difference from the checked baseline. A
  separate precompiled baseline/current/current/baseline control with ten
  measurements per variant gives cache-store p50 1,522.5 to 1,585 ns (+4.11%)
  and cache-hit p50 1,169 to 1,173 ns (+0.34%). Retained evidence:
  `.build/benchmarks/task519-action/`. This does not erase the native +23.20%
  absolute timing failure or establish its cause. No unchanged CI rerun or
  speculative cache implementation change was made to obtain a green result.
- Compiler and comparison implementation and native correctness verification
  are complete. The repeated absolute observations are transferred to TASK 520;
  the overall thirty-item completion claim remains open.
- Controlled precompiled before/after/after/before comparison, six measurements
  per variant at CPU 1 and 250ms, is retained with binaries, source patch, and
  SHA-256 identities in `.build/benchmarks/task519-compiler/`. Maximum observed
  process RSS falls 108,740,608 to 36,372,480 bytes (-66.55%). Unique-rule median
  time falls 4,018,413.5 to 1,366,067.5 ns (-66.00%) and B/op 12,139,672 to
  1,492,688 (-87.70%). Grouped-duplicate time falls 10,465,366.5 to 9,615,603.5 ns
  (-8.12%) and B/op 20,924,336 to 20,284,960 (-3.06%). Allocation counts are
  unchanged. These are compiler workload measurements, not whole-CLI claims.
- The corrected source borrows read-only rule pointers in both indexes. The
  existing conflict suite and new four-kind input/result ownership regression
  pass. Full root/template race suites, vet, and staticcheck pass. After the
  historical-note correction below, `make test-release-trust` passes, including
  publication/reference checks and the real isolated release target (71s).
- Warm-prefix source inspection finds no change to the measured identity/path
  checking implementation. Native p95 improves while raw p50 and the ratio
  worsen; earlier controlled local p50 improves. The retained profile is mostly
  filesystem system calls. These data do not prove a single production cause;
  no freshness, path, evidence, or cancellation check is removed.
- Improving the unique-rule calibration path more than the duplicate target
  raises the target/calibration ratios despite improvements in both workloads.
  The approved comparison contract additionally requires target degradation
  beyond that normalized metric's tolerance; every existing absolute limit and
  recorded ratio remains visible.
- Root and portable-template race suites, publication/reference checks, vet,
  and staticcheck pass. The final release-trust stage rejects numeric coverage
  observations in project text, including historical TASK notes. TASK 518 now
  links the original native measurement log instead; the guard is unchanged.
- All grouping/selection consumers are confined to `internal/compiler/conflicts.go`.
  Preserve the exported `DetectConflicts([]policy.Rule)` boundary and reference
  caller-owned rules only during its read-only invocation. Index by kind and
  semantic key using rule pointers; keep every outward path slice cloned and
  all normalized-list sorting on copies. No retained global index is introduced.
- Checked source: c814f5b40579d2feed47231ec9dc2d80afda1dea. Paired candidate:
  6116c64d8b6fe9f391177368ba851e450d09f8ed. Evidence remains in
  `.build/benchmarks/task518-ci-34597552388/` and
  `.build/benchmarks/task518-diagnostics/`.
- The inspected predecessor copied `policy.Rule` values in `byKind` and again
  in semantic groups. All consumers are internal, read-only, and clone returned
  path slices. Inspect pointer/index reuse before adding any new abstraction.
- Warm-prefix native raw p50 is +18.33%, normalized +33.05%, p95 -4.61%; local
  controlled prior samples are -2.24%. These observations do not establish one
  causal explanation. Never rerun unchanged CI merely to seek a green result.

## Deviations

- Native verification requires publishing a locally verified candidate on main
  under the standing push instruction. Remaining measurement work is fully
  split into TASK 520 after native verification; this archive does not claim a
  passing performance comparison.
