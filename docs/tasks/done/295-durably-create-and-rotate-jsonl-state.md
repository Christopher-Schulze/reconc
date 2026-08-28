# TASK 295: Durably create and rotate JSONL state

## Why

JSONL syncs record bytes but not parent-directory entries after live-file creation, archive rename/removal, or journal-related rotation. Ledger and audit users can lose a reported append or observe an incompletely persisted archive ring after a crash.

## Acceptance

- New live files, archive renames/removals, and rotation completion have ordered parent-directory durability barriers.
- Journal recovery remains idempotent across every injected crash point.
- Existing layout security, append atomicity, archive bounds, and cross-process locking remain unchanged.
- JSONL, action-ledger, audit, retention, race, and platform tests pass.

## Sub-Tasks

- [x] Specify the JSONL rotation durability state machine
- [x] Add portable parent-sync boundaries
- [x] Add create and every-rename crash regressions
- [x] Run all JSONL consumers and recovery gates

## Notes

- Evidence: `internal/jsonl/journal.go:32-53` and `internal/jsonl/append.go:268-316`.
- Ordered commit points are: sync a newly created live-file entry after its
  payload; sync every archive removal or rooted rename before the next ring
  mutation; and sync backup/journal cleanup before declaring rotation resolved.
  Recovery may therefore replay from any journal state without depending on a
  later directory mutation to persist an earlier one.
- Verification: focused crash-point tests, complete JSONL/action-ledger/audit/
  retention tests and race suites, Windows amd64 test compilation and vet,
  `make test`, `make vet`, `make lint`, and `make self-host` all passed.

## Deviations

None.
