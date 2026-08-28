# TASK 295: Durably create and rotate JSONL state

## Why

JSONL syncs record bytes but not parent-directory entries after live-file creation, archive rename/removal, or journal-related rotation. Ledger and audit users can lose a reported append or observe an incompletely persisted archive ring after a crash.

## Acceptance

- New live files, archive renames/removals, and rotation completion have ordered parent-directory durability barriers.
- Journal recovery remains idempotent across every injected crash point.
- Existing layout security, append atomicity, archive bounds, and cross-process locking remain unchanged.
- JSONL, action-ledger, audit, retention, race, and platform tests pass.

## Sub-Tasks

- [ ] Specify the JSONL rotation durability state machine
- [ ] Add portable parent-sync boundaries
- [ ] Add create and every-rename crash regressions
- [ ] Run all JSONL consumers and recovery gates

## Notes

- Evidence: `internal/jsonl/journal.go:32-53` and `internal/jsonl/append.go:268-316`.

## Deviations

None.
