# TASK 317: Reduce decimal parse and render allocation

## Why

Every JSON number is reparsed with a regular-expression submatch allocation, normalized through string concatenation, then rendered to a temporary string before append. Constant zero/one bounds are reparsed repeatedly by inspection helpers.

## Acceptance

- Decimal parsing remains exact, non-floating-point, bounded, and rejects every invalid JSON number currently rejected.
- Rendering appends directly to the destination buffer without intermediate exponent strings where possible.
- Common zero/one bounds avoid repeated parsing without exposing mutable global state.
- Differential, fuzz, canonical JSON, and benchmark tests prove semantic parity and measured allocation reduction.

## Sub-Tasks

- [ ] Benchmark parse and render allocation sources
- [ ] Implement a bounded direct decimal scanner
- [ ] Add direct append rendering and immutable common bounds
- [ ] Run numeric fuzz, schema, action, and benchmark gates

## Notes

- Evidence: `internal/action/value.go:71-121,543-560` and repeated bounds in `internal/actioninspect/mcp_result.go`.

## Deviations

None.
