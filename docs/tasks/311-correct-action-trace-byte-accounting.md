# TASK 311: Correct action trace byte accounting

## Why

Trace collection starts at two bytes for `[]` and then charges one separator for every entry, including the first. The retained JSON size is overcounted by one byte, and overflow handling re-marshals retained entries while ignoring encoding errors.

## Acceptance

- Logical trace bytes equal the exact compact JSON array size for zero, one, and maximum entries.
- Overflow marker insertion reuses exact retained entry sizes and propagates impossible encoding failures.
- Prefix retention, omitted counts, deterministic ordering, and published limits remain unchanged.
- Boundary, allocation, fuzz, and evaluator tests pass.

## Sub-Tasks

- [ ] Define exact array delimiter accounting
- [ ] Retain encoded size metadata without duplicating trace payloads
- [ ] Make overflow finalization error-aware
- [ ] Add one-byte-boundary and maximum-trace regressions

## Notes

- Evidence: `internal/action/evaluator.go:754-807`.

## Deviations

None.
