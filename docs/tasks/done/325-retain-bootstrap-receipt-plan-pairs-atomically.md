# TASK 325: Retain bootstrap receipt-plan pairs atomically

## Why

Bootstrap retention removes an obsolete receipt and then discards the result of deleting its paired plan. A failed second removal leaves an orphan plan with no surfaced recovery signal.

## Acceptance

- Retention treats each validated receipt and plan as one pair with a recoverable deletion protocol.
- Failure at either removal boundary is observable and cannot silently strand an unowned counterpart.
- Concurrent replacement or content mutation preserves both user-owned files and reports the skipped pair.
- Retention remains bounded, best-effort at its callers, and covered by deterministic fault injection.

## Sub-Tasks

- [x] Define paired retention outcomes and recovery order
- [x] Preserve and report partial-removal state safely
- [x] Add failure-at-each-boundary tests
- [x] Run bootstrap retention and recovery gates

## Notes

- Evidence: `internal/bootstrap/receipt_retention.go` validates and snapshots both
  pair members before mutation, reports every failed pair, and restores deleted
  members after a later boundary failure. `internal/bootstrap/receipt_retention_test.go`
  covers receipt-boundary failure, plan-boundary rollback, and concurrent plan
  mutation. Apply summaries and repository-sync reports surface bounded warnings.

## Deviations

None.
