# TASK 395: Prevent TASK section-counter overflow

## Why

The fast run-state scanner counts section headers in `uint8`. A malformed overview with 257 copies of a required header wraps back to one and can pass the exact-one check used by Stop decisions.

## Acceptance

- Section counts cannot overflow into an accepted value for any bounded input.
- The fast scanner rejects every duplicate required section exactly as the full parser does.
- Large malformed overviews remain bounded in time and memory.
- Differential tests cover 2, 255, 256, 257, and maximum-size duplicate headers.

## Sub-Tasks

- [x] Replace wrapping counters with saturating or non-overflowing state.
- [x] Add fast/full parser parity fixtures at overflow boundaries.
- [x] Verify valid boards retain the fast path.
- [x] Run focused task-lifecycle tests.

## Notes

- Verified from finding 53 and its duplicate finding 164.
- `scanActiveSectionsRows` uses `[5]uint8` and later accepts a section count only when it equals one.
- Current-code reproduction: the new differential regression failed only at 257 `## Active` headings before the fix because the `uint8` counter wrapped to one and the fast path returned a valid `RunContinue` state; the complete parser still returned `task/overview/duplicate-section`.
- The fast scanner now stores one boolean per required lifecycle section and returns fallback immediately on the second heading, eliminating arithmetic and bounding duplicate work independently of the overview size.
- Focused fast/full parity cases for 2, 255, 256, 257, and an exact 4 MiB malformed overview passed; the existing valid-board fast-path regression and the complete `internal/tasklifecycle` package also passed.
- `make test-fast` passed for the root and portable-template modules.

## Deviations
