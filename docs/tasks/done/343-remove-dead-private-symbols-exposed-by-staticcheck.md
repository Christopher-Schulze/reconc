# TASK 343: Remove dead private symbols exposed by staticcheck

## Why

The repository lint gate currently fails on five private helpers or methods
that have no callers after earlier refactors. Keeping unreachable code creates
maintenance surface and prevents the required static-analysis gate from
providing a clean signal.

## Acceptance

- The five reported U1000 symbols are removed only when repository-wide search
  proves they have no callers.
- No exported API, behavior, test coverage, or public contract changes.
- `make lint` passes and focused package tests remain green.
- Documentation and task state are flushed before commit.

## Sub-Tasks

- [x] Verify each symbol and its replacement/current owner
- [x] Remove only dead private declarations
- [x] Run lint and focused tests
- [x] Archive this TASK with evidence

## Notes

- `make lint` reported `PublicationResult.published`, `readBoundedBackup`,
  `reportHash`, `recordDigest`, and `acceptsContract` as U1000.
- Repository-wide search found no callers for those five private declarations;
  the active replacements are `PublicationResult` outcome fields,
  `readBoundedBackupWithLayout`, `recordDigestWithReportBytes`, and indexed
  schema lookup.
- Focused evidence: `go test ./internal/atomicfile ./internal/jsonl ./internal/policyproof ./internal/schema -count=1` and `make lint` passed.

## Deviations

None.
