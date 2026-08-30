# TASK 382: Streamline command semantic normalization

## Why

`normalizeCommandSemantics` performs several complete scans and builds multiple intermediate strings for every observed command on a hot policy path.

## Acceptance

- Command semantics remain byte-for-byte equivalent for quoting, whitespace, separators, paths, and malformed input.
- Normalization uses one bounded scan or otherwise demonstrably reduces passes and allocations.
- Table and fuzz regressions cover current edge cases and parser boundaries.
- Benchmarks prove lower allocations and runtime for simple and complex commands.

## Sub-Tasks

- [x] Capture current normalization golden cases and benchmark baselines.
- [x] Implement a bounded single-state scan without changing accepted syntax.
- [x] Add differential and fuzz regressions.
- [x] Run focused runtime tests and benchmarks.

## Notes

- Verified from finding 13.
- Current simple and complex benchmark cases allocate six to eight objects per normalization.
- Apple M1 baseline medians: simple 558 ns/op, 208 B/op, 6 allocs/op; wrapped 710 ns/op, 224 B/op, 6 allocs/op; complex 1,797 ns/op, 340 B/op, 8 allocs/op.
- The fused scanner median is simple 184 ns/op, 32 B/op, 2 allocs/op; wrapped 228 ns/op, 40 B/op, 2 allocs/op; complex 521 ns/op, 80 B/op, 2 allocs/op. That is 67-71% lower runtime, 76-85% fewer bytes, and 67-75% fewer allocations across the captured cases.
- A test-only copy of the prior algorithm is the differential oracle for fixed malformed/parser boundaries and the fuzz target. A five-second run covered 16,674 executions without drift.
- Focused normalization tests, the complete uncached runtime package, formatter/reference checks, and `make test-fast` pass on macOS.

## Deviations
