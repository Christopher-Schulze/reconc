# TASK 382: Streamline command semantic normalization

## Why

`normalizeCommandSemantics` performs several complete scans and builds multiple intermediate strings for every observed command on a hot policy path.

## Acceptance

- Command semantics remain byte-for-byte equivalent for quoting, whitespace, separators, paths, and malformed input.
- Normalization uses one bounded scan or otherwise demonstrably reduces passes and allocations.
- Table and fuzz regressions cover current edge cases and parser boundaries.
- Benchmarks prove lower allocations and runtime for simple and complex commands.

## Sub-Tasks

- [ ] Capture current normalization golden cases and benchmark baselines.
- [ ] Implement a bounded single-state scan without changing accepted syntax.
- [ ] Add differential and fuzz regressions.
- [ ] Run focused runtime tests and benchmarks.

## Notes

- Verified from finding 13.
- Current simple and complex benchmark cases allocate six to eight objects per normalization.

## Deviations
