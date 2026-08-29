# TASK 390: Retain pre-decision and taint-resolution state

## Why

Per-project `pre-decisions` and `evidence-taint-resolutions` directories are outside every retention class and aggregate state-byte budget. Long-lived repositories can therefore accumulate these private artifacts without bound.

## Acceptance

- Both artifact classes have explicit age, count, and byte budgets included in project state totals.
- Active-session pre-decision entries and audit-relevant taint resolutions remain protected.
- Candidate discovery is bounded, identity-revalidated, symlink-safe, and dry-run truthful.
- Tests cover age, pressure, active protection, malformed entries, concurrent replacement, and total accounting.

## Sub-Tasks

- [ ] Define retention policies and active-object predicates for both classes.
- [ ] Integrate them into class and aggregate accounting.
- [ ] Add adversarial retention regressions.
- [ ] Run focused retention and agent-session tests.

## Notes

- Verified from finding 39 and its duplicate finding 187.
- Current retention classes cover sessions, reports, locks, command proofs, and policy decisions but not these two directories.

## Deviations
