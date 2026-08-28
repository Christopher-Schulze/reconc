# TASK 323: Replace audit error-string protocols with typed classification

## Why

Audit recovery decides whether to try the legacy JSONL layout by searching an error message for `belongs to a different layout`. Wording changes or wrapped foreign errors can silently change control flow.

## Acceptance

- Layout mismatch has a typed sentinel or structured error classification.
- Legacy fallback occurs only for that exact condition and no other recovery failure.
- Wrapped errors preserve path and operation context while remaining classifiable with `errors.Is` or `errors.As`.
- Current, legacy, corrupt, foreign-layout, and wording-independent tests pass.

## Sub-Tasks

- [ ] Define the JSONL layout mismatch error contract
- [ ] Replace audit substring branching
- [ ] Add wrapped and lookalike error regressions
- [ ] Run audit, JSONL, migration, and recovery gates

## Notes

- Evidence: `internal/audit/audit.go:604-626`.

## Deviations

None.
