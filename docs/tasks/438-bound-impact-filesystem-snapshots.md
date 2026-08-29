# TASK 438: Bound impact filesystem snapshots

## Why

Impact snapshot capture limits entry count but not individual or aggregate file bytes. A large corpus can be fully read and then read again during revalidation, producing unbounded memory and I/O within the admitted file count.

## Acceptance

- Snapshot capture enforces explicit per-file and aggregate byte budgets before allocation.
- Limit exhaustion is reported deterministically and never yields a partial snapshot presented as complete.
- Revalidation reuses safe identities or bounded reads without weakening change detection.
- Benchmarks and adversarial tests cover many small files, one huge file, aggregate overflow, mutation, and maximum accepted input.

## Sub-Tasks

- [ ] Measure current snapshot allocations and bytes read at boundaries.
- [ ] Add pre-read size accounting and aggregate admission.
- [ ] Preserve identity revalidation under bounded I/O.
- [ ] Run focused impact tests and benchmarks.

## Notes

- Verified from finding 165 in `internal/impactlab/filesystem_snapshot.go`.

## Deviations
