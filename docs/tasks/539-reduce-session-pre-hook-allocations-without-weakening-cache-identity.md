# TASK 539: Reduce session pre-hook allocations without weakening cache identity

## Why

TASK 538's complete comparable benchmark run found four remaining relative allocation regressions in the warm-prefix and cold-prefix session-hook workloads. The increase already exists before TASKs 532-537 and is attributable to TASK 525's dependency planning. The six review repairs must remain intact.

## Acceptance

- Both session-hook targets meet the existing relative byte and allocation limits against the exact checked baseline, with clean source identities and unchanged tolerances.
- Preserve every TASK 525 dependency, content/generation identity, root/path validation, operational-error exclusion, and before/after mutation barrier; do not substitute metadata-only identity or skip malformed input validation.
- Preserve TASK 533 declared prospective writes, alias and symlink retargeting, native approval exclusions, and completed-evidence separation.
- Meaningful cache/dependency/normalization regression tests, complete repository gates, and the full comparable benchmark suite pass; report time, per-operation allocation, and process peak memory separately.

## Sub-Tasks

- [ ] Reproduce the committed TASK 538 measurements and inspect the allocation profiles at the exact TASK 525 boundary.
- [ ] Read PreDecisionDependencies, normalizeEvaluationInput, preDecisionEvaluationInputs, capturePreDecisionObservedIdentityWithEvaluatorAndStopCache, and their callers and invalid-input contracts.
- [ ] Remove redundant normalization or observation only where an existing validated immutable value can be reused within the same sample; evaluate empty-route handling without bypassing validation or the second observation barrier.
- [ ] Add behavioral regressions for the chosen reuse boundary, including same-size/same-mtime changes, path replacement, stale sources, malformed declarations, dependency bounds, and operational failures.
- [ ] Measure both session workloads and all 19 benchmark groups with unchanged parameters and limits; run repository gates, update relevant documentation, archive, commit, and push only under execution authority.

## Notes

- Discovered during TASK 538 verification; queued separately under the discovered-issue lifecycle. Implementation has not started.
- Checked baseline: `c814f5b40579d2feed47231ec9dc2d80afda1dea`. Clean measured candidate: `7c5a666ae11d84cd3e348418267f9781706c97c3`.
- Warm-prefix: 643342 to 684886 B/op (relative +6.4575%), 5585 to 5906 allocs/op (+5.7475%). Cold-prefix: 725409 to 766701 B/op (+5.6922%), 6152 to 6470 allocs/op (+5.1691%). Relative limits remain 5%; absolute allocation limits remain 10%. CPU-adjusted time and process peak-RSS limits pass.
- Five repeated measurements at pre-repair source `70117feaa45b85d045670219d598fe9d1f1428e8` reproduce the current allocation counts and byte totals. TASK 525 parent `d0e5a2a6d27c6689ba958620b0ff546b754a7e43` uses approximately 643804 B/op and 5594 allocs/op warm; TASK 525 commit `cb71a3b7a33bd651da95875632dfbdb422ef4aff` uses approximately 684857 B/op and 5906 allocs/op warm.
- Fixed-work allocation profiles across that boundary attribute approximately 3991 KiB additional cumulative allocation to PreDecisionDependencies, including approximately 3889 KiB in normalizeEvaluationInput, across the profile's repeated workload and setup. These are profile totals, not B/op. Additional path resolution and command normalization dominate the delta even in the empty-rule fixture.
- Full comparison: `.build/benchmarks/task538-comparison.json`; raw results and hashes are adjacent. Attribution logs and profiles: `.build/review-repairs/task538-before-525-*`, `task538-after-525-*`, `task538-before-repairs-session.log`, and `task538-allocation-cumulative-diff.txt`.

## Deviations

None.
