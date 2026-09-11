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
- [~] Run local/native gates, inspect the full comparison, flush documentation, archive, commit, and push.

## Notes

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
  raises the current target/calibration ratios despite improvements in both
  workloads. The existing comparison contract is unchanged. Christopher's
  decision is pending on whether normalized violations should additionally
  require target degradation beyond that normalized metric's tolerance; every
  existing absolute limit and recorded ratio would remain visible.
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
- Compiler indexing currently copies `policy.Rule` values in `byKind` and again
  in semantic groups. All consumers are internal, read-only, and clone returned
  path slices. Inspect pointer/index reuse before adding any new abstraction.
- Warm-prefix native raw p50 is +18.33%, normalized +33.05%, p95 -4.61%; local
  controlled prior samples are -2.24%. These observations do not establish one
  causal explanation. Never rerun unchanged CI merely to seek a green result.

## Deviations

- Native verification requires publishing a locally verified candidate on main
  under the standing push instruction. Keep this TASK active until native
  evidence and the pending comparison-contract decision are resolved.
