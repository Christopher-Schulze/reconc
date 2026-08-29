# TASK 430: Share builtin secret-detector programs

## Why

Each scanner and engine factory recompiles the immutable built-in detector pack, repeating regular-expression compilation and allocations for identical process-wide programs.

## Acceptance

- Built-in detector programs compile once per process and are immutable to all callers.
- Factory/scanner construction performs no duplicate built-in regex compilation.
- Concurrent scanners cannot mutate shared detector state.
- Benchmarks prove lower construction allocations and latency with identical matches and ordering.

## Sub-Tasks

- [ ] Measure factory and scanner construction baselines.
- [ ] Publish one immutable built-in pack through an initialization path with explicit error handling.
- [ ] Add concurrency, immutability, and match-parity tests.
- [ ] Run focused scanner tests and benchmarks.

## Notes

- Verified from finding 113 in `internal/actioninspect` construction paths.

## Deviations
