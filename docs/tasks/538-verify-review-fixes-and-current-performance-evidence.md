# TASK 538: Verify review fixes and current performance evidence

## Why

The six repairs need a final integrated proof and current comparable performance measurements; the prior full suite predates TASK 525 and Cursor implementation.

User-approved review item: Completion. Execute on main; commit and push after completion. Product tags and releases remain outside this task.

## Acceptance

- All six reported defects are closed with meaningful regression tests.
- Integrated repository gates pass and benchmark results identify clean source revisions and comparable environments.
- Performance claims distinguish per-operation allocation metrics from process peak memory and disclose any remaining regression.
- Historical details remain preserved, the overview follows the ten-entry contract, and remote state matches the committed deliverables.

## Sub-Tasks

- [ ] Verify every R1-R6 acceptance against committed code, regression evidence, archived details, and remote commit identities.
- [ ] Run the integrated test, vet, lint, development build, whole-module coverage, isolated self-host, and publication checks.
- [ ] Record the complete benchmark suite for a clean code candidate and comparable baseline with attributable source and environment; assess time, allocated bytes, allocation count, and process peak memory separately.
- [ ] Investigate measured regressions before claiming completion; preserve truthful evidence and avoid changing thresholds merely to make results green.
- [ ] Keep only ten Done entries, verify retention of all 25 ignored original historical details, and explicitly document their local status without publishing private planning material.
- [ ] Flush task-relevant documentation, re-read final files, archive, commit, push origin/main, and verify the persisted remote head.

## Notes

- Starting source: 70117feaa45b85d045670219d598fe9d1f1428e8.
- Source boundaries: Makefile; scripts/benchmarks/history; docs/documentation.md; docs/tasks.md.
- Use isolated repository fixtures; never run repository-targeted Reconc commands against the product root.
- Planning details are explicitly tracked for this approved work despite the existing local-task ignore rule.

## Deviations

None.
