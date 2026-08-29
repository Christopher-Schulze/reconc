# TASK 395: Prevent TASK section-counter overflow

## Why

The fast run-state scanner counts section headers in `uint8`. A malformed overview with 257 copies of a required header wraps back to one and can pass the exact-one check used by Stop decisions.

## Acceptance

- Section counts cannot overflow into an accepted value for any bounded input.
- The fast scanner rejects every duplicate required section exactly as the full parser does.
- Large malformed overviews remain bounded in time and memory.
- Differential tests cover 2, 255, 256, 257, and maximum-size duplicate headers.

## Sub-Tasks

- [ ] Replace wrapping counters with saturating or non-overflowing state.
- [ ] Add fast/full parser parity fixtures at overflow boundaries.
- [ ] Verify valid boards retain the fast path.
- [ ] Run focused task-lifecycle tests.

## Notes

- Verified from finding 53 and its duplicate finding 164.
- `scanActiveSectionsRows` uses `[5]uint8` and later accepts a section count only when it equals one.

## Deviations
