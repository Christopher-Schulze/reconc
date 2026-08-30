# TASK 438: Bound impact filesystem snapshots

## Why

Impact snapshot capture limits entry count but not individual or aggregate file bytes. File contents are streamed, so allocation is not proportional to content size, but a large corpus can be fully read and then read again during revalidation, producing unbounded I/O within the admitted file count.

## Acceptance

- Snapshot capture enforces explicit per-file and aggregate byte budgets before allocation.
- Limit exhaustion is reported deterministically and never yields a partial snapshot presented as complete.
- Revalidation reuses safe identities or bounded reads without weakening change detection.
- Benchmarks and adversarial tests cover many small files, one huge file, aggregate overflow, mutation, and maximum accepted input.

## Sub-Tasks

- [x] Measure current snapshot allocations and bytes read at boundaries.
- [x] Add pre-read size accounting and aggregate admission.
- [x] Preserve identity revalidation under bounded I/O.
- [x] Run focused impact tests and benchmarks.

## Notes

- Verified from finding 165 in `internal/impactlab/filesystem_snapshot.go`.
- Baseline `BenchmarkRepositorySnapshotManySmallFiles` over 256 x 1 KiB files: 6.49 ms/op, 40.38 MB/s, 818,379 B/op, and 4,432 allocs/op on darwin/arm64. File contents are streamed rather than retained, so the confirmed defect is unbounded aggregate I/O, including the second revalidation read, rather than allocation proportional to file bytes.
- Capture now admits at most 64 MiB per regular file and 512 MiB across at most 100,000 entries before hashing. Revalidation retains the original snapshot limits and rehashes every admitted identity, preserving same-metadata content-drift detection.
- Post-change 5-iteration evidence: the 64 MiB maximum accepted regular file completed at 134.96 ms/op, 497.26 MB/s, 41,368 B/op, and 76 allocs/op; the many-small-files benchmark completed at 17.55 ms/op, 14.94 MB/s, 818,428 B/op, and 4,432 allocs/op. Wall time was filesystem-noisy; allocation remained content-independent.

## Deviations
