# TASK 188: Batch prospective path identity resolution

## Why

`pathidentity.ResolveProspective` walks ancestors until it finds an existing
component. Runtime normalization invokes it independently for many paths that
share the same repository root and directory prefixes, repeating `Lstat`,
resolution, and containment work.

## Acceptance

- A batch API or evaluation-scoped resolver reuses only proven-safe ancestor
  identities and returns one result per original input in deterministic order.
- Symlink/reparse-point detection, missing-component handling, case behavior,
  containment, and errors remain equivalent to independent resolution.
- Cached ancestors are invalidated or revalidated when filesystem identity can
  change during the operation; no string-only security shortcut is introduced.
- Tests cover shared prefixes, missing ancestors, swaps, symlinks, Windows
  reparse behavior, duplicates, and partial failures.
- Benchmarks prove fewer syscalls for realistic path sets.

## Sub-Tasks

- [x] Specify security-preserving batch resolution semantics
- [x] Implement ancestor identity reuse with bounded state
- [x] Migrate high-cardinality runtime callers
- [x] Add differential, adversarial, platform, and benchmark tests
- [x] Run pathidentity, runtime, race, and complete gates

## Notes

- Added evaluation-scoped `ProspectiveResolver` and ordered batch APIs. The
  resolver caches at most 4,096 existing ancestors, re-Lstats and compares
  identity/mode/size/modification time before reuse, and never caches missing
  suffixes. Cache state is not process-global.
- Runtime path normalization shares one resolver across each read/write batch;
  duplicate order and prospective containment behavior remain unchanged.
- Differential and ancestor-replacement tests pass. Apple M1 benchmark:
  shared-prefix batch 54.874 us/op, 9,272 B/op, 88 allocs versus independent
  resolution 88.793 us/op, 16,728 B/op, 171 allocs.

## Deviations

None.
