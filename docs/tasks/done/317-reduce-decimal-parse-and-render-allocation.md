# TASK 317: Reduce decimal parse and render allocation

## Why

Every JSON number is reparsed with a regular-expression submatch allocation, normalized through string concatenation, then rendered to a temporary string before append. Constant zero/one bounds are reparsed repeatedly by inspection helpers.

## Acceptance

- Decimal parsing remains exact, non-floating-point, bounded, and rejects every invalid JSON number currently rejected.
- Rendering appends directly to the destination buffer without intermediate exponent strings where possible.
- Common zero/one bounds avoid repeated parsing without exposing mutable global state.
- Differential, fuzz, canonical JSON, and benchmark tests prove semantic parity and measured allocation reduction.

## Sub-Tasks

- [x] Benchmark parse and render allocation sources
- [x] Implement a bounded direct decimal scanner
- [x] Add direct append rendering and immutable common bounds
- [x] Run numeric fuzz, schema, action, and benchmark gates

## Notes

- Evidence: `internal/action/value.go:76-240,650-663`, `internal/actioninspect/mcp_result.go:546-601`, and `internal/mcpgateway/progress.go:322`.
- `ParseDecimal` now validates the JSON number grammar and exponent range in one bounded byte scan, normalizes digit spans without regex submatches or temporary concatenation, and retains exact integer arithmetic.
- Decimal JSON appends now use the caller's buffer directly; representative rendering measured 0 B/op and 0 allocs/op versus 16 B/op and 1 alloc/op for `Decimal.String` across three 100-iteration samples.
- `ZeroDecimal` and `OneDecimal` return immutable value copies; MCP result and progress validation reuse them, and integer validation no longer renders a temporary decimal string.
- Verification: decimal boundary/invalid-syntax/append regressions, JSON differential tests, Decimal comparison fuzz (8s), action/action-inspection/MCP gateway tests and races, numeric benchmarks, `make test-fast`, `make vet`, Staticcheck v0.8.1, and `make self-host` all passed.

## Deviations

None.
