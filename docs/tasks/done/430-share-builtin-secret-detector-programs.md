# TASK 430: Share builtin secret-detector programs

## Why

Each scanner and engine factory recompiles the immutable built-in detector pack, repeating regular-expression compilation and allocations for identical process-wide programs.

## Acceptance

- Built-in detector programs compile once per process and are immutable to all callers.
- Factory/scanner construction performs no duplicate built-in regex compilation.
- Concurrent scanners cannot mutate shared detector state.
- Benchmarks prove lower construction allocations and latency with identical matches and ordering.

## Sub-Tasks

- [x] Measure factory and scanner construction baselines.
- [x] Publish one immutable built-in pack through an initialization path with explicit error handling.
- [x] Add concurrency, immutability, and match-parity tests.
- [x] Run focused scanner tests and benchmarks.

## Notes

- Verified from finding 113 in `internal/actioninspect` construction paths.
- A single `sync.OnceValues` initialization compiles and validates the pack;
  constructors return immutable process-wide scanner/factory views and retain
  the cached error contract.
- Corpus parity covers every detector fixture. Concurrent factory, engine, and
  scanner construction shares the same programs and leaves their semantic
  signature unchanged.
- Apple M1 construction baseline: scanner 362,664-368,533 ns/op,
  923,850-924,831 B/op, 5,105 allocs/op; factory 370,578-373,031 ns/op,
  921,180-924,233 B/op, 5,104-5,105 allocs/op.
- Final benchmark: scanner 2.726-2.729 ns/op and factory 2.724-2.727 ns/op,
  both 0 B/op and 0 allocs/op. The complete `internal/actioninspect` test
  package and focused construction/concurrency tests passed.

## Deviations
