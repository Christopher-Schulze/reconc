# TASK 189: Batch write-epoch path normalization

## Why

Write-epoch normalization invokes the general path normalization pipeline with
a one-element slice for every map entry. This repeats allocations, root
resolution, ancestor traversal, and normalization setup while later merging
aliases by maximum epoch.

## Acceptance

- All raw write-epoch keys are normalized in one batch with a deterministic
  raw-to-normalized mapping.
- Alias collisions preserve the current maximum-epoch rule and never lose a
  write identity or silently convert an invalid path.
- Root containment and prospective-path security are identical to other write
  path normalization.
- Tests cover duplicates, aliases, mixed separators, missing paths, invalid
  paths, map-order independence, and maximum-epoch behavior.
- Benchmarks show reduced allocations and path-resolution calls at configured
  maximum cardinality.

## Sub-Tasks

- [x] Capture current write-epoch normalization invariants
- [x] Implement one batch normalization pass
- [x] Preserve deterministic alias and error handling
- [x] Add regression and benchmark coverage
- [x] Run runtime and complete Go gates

## Notes

- `normalizeWriteEpochsWithResolvedRoot` now uses one prospective resolver and
  one direct path-normalization pass for the ordered write sequence. Empty/root
  entries remain omitted, invalid paths fail at the same boundary, and alias
  collisions retain the maximum epoch.
- Regression coverage verifies absolute/relative aliases and maximum-epoch
  preservation. Apple M1 benchmark for 128 paths: batch 917.368 us/op,
  267,835 B/op, 2,087 allocs versus per-path 3.354 ms/op, 665,602 B/op,
  6,656 allocs.

## Deviations

None.
