# TASK 209: Expose one stable bounded assurance-file read

## Why

Assurance file inspection performs metadata checks for eligibility and budget
charging, then calls a bounded regular-file reader that repeats identity and
metadata checks around open/read. The duplication protects against swaps, but
the API forces callers to choose between repeated syscalls and unsafe removal
of checks.

## Acceptance

- `boundedio` exposes a stable regular-file snapshot operation that returns
  opened metadata and bounded bytes from the same verified inode.
- Assurance charges file count and bytes exactly once from that snapshot and
  never trusts a pre-open path stat for content identity.
- Before/open/after identity, size, type, symlink, truncation, growth, and read
  error behavior remains fail closed on Unix and Windows.
- Existing callers that need only bytes remain simple wrappers over the same
  primitive instead of duplicating security logic.
- Tests count metadata operations and exercise concurrent replacement;
  benchmarks demonstrate the reduction.

## Sub-Tasks

- [x] Specify the bounded stable-file snapshot return contract
- [x] Implement it once in `boundedio`
- [x] Migrate assurance budget and read handling
- [x] Add race, platform, syscall-count, and benchmark tests
- [ ] Run boundedio, assurance, race, and complete gates

## Notes

- `boundedio.ReadRegularFileSnapshot` now returns the opened regular-file
  metadata and bounded bytes from one `WithRegularFileSnapshot` transaction;
  `ReadRegularFile` is a compatibility wrapper over that primitive. Existing
  before/open/after identity, mode, size, symlink, truncation, growth, and
  close/read error checks remain centralized.
- Assurance `readBounded` charges the opened snapshot's file and byte identity,
  with per-path byte charging deduplicated in the scan budget. It no longer
  performs a pre-open `Lstat` whose metadata could be confused with the bytes
  actually read.
- Focused tests cover strict opened identity, same-size mutation rejection,
  exact limits, irregular/symlink/FIFO rejection, and exactly-once assurance
  budget charging. Full race and complete gates remain for queue completion.

## Deviations

None.
