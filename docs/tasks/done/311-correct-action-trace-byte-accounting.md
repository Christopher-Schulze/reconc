# TASK 311: Correct action trace byte accounting

## Why

Trace collection starts at two bytes for `[]` and then charges one separator for every entry, including the first. The retained JSON size is overcounted by one byte, and overflow handling re-marshals retained entries while ignoring encoding errors.

## Acceptance

- Logical trace bytes equal the exact compact JSON array size for zero, one, and maximum entries.
- Overflow marker insertion reuses exact retained entry sizes and propagates impossible encoding failures.
- Prefix retention, omitted counts, deterministic ordering, and published limits remain unchanged.
- Boundary, allocation, fuzz, and evaluator tests pass.

## Sub-Tasks

- [x] Define exact array delimiter accounting
- [x] Retain encoded size metadata without duplicating trace payloads
- [x] Make overflow finalization error-aware
- [x] Add one-byte-boundary and maximum-trace regressions

## Notes

- Evidence: `internal/action/evaluator.go:754-807`.
- The collector must account for `[` and `]` plus separators only between entries; a fixed-size parallel length table avoids retained-entry re-encoding and additional hot-path allocations.
- Finalization errors must become an internal-invariant evaluation failure rather than a partial or falsely complete trace.
- Exact-size, one-byte-overflow, maximum-entry, evaluator, repeated allocation-path, and evaluator fuzz tests passed; `make test`, `make vet`, Staticcheck v0.8.1, and `make self-host` are green.

## Deviations

None.
