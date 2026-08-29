# TASK 323: Replace audit error-string protocols with typed classification

## Why

Audit recovery decides whether to try the legacy JSONL layout by searching an error message for `belongs to a different layout`. Wording changes or wrapped foreign errors can silently change control flow.

## Acceptance

- Layout mismatch has a typed sentinel or structured error classification.
- Legacy fallback occurs only for that exact condition and no other recovery failure.
- Wrapped errors preserve path and operation context while remaining classifiable with `errors.Is` or `errors.As`.
- Current, legacy, corrupt, foreign-layout, and wording-independent tests pass.

## Sub-Tasks

- [x] Define the JSONL layout mismatch error contract
- [x] Replace audit substring branching
- [x] Add wrapped and lookalike error regressions
- [x] Run audit, JSONL, migration, and recovery gates

## Notes

- Evidence: `internal/audit/audit.go:604-626`.
- Added `jsonl.ErrLayoutMismatch`; journal errors retain the journal path and
  remain classifiable through `errors.Is`.
- Audit recovery now retries the generic layout only for that typed condition.
- Verified with `go test ./internal/jsonl`, `go test ./internal/audit`, and the
  focused lookalike regression.

## Deviations

None.
