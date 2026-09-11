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

- [ ] Inspect conflict-index ownership and every caller; measure the exact unchanged workload.
- [ ] Remove proven redundant rule copies and verify output parity and input/output isolation.
- [ ] Complete bounded warm-prefix attribution with preserved raw/profile evidence.
- [ ] Run local/native gates, inspect the full comparison, flush documentation, archive, commit, and push.

## Notes

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

None.
