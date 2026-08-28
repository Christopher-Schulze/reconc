# TASK 325: Retain bootstrap receipt-plan pairs atomically

## Why

Bootstrap retention removes an obsolete receipt and then discards the result of deleting its paired plan. A failed second removal leaves an orphan plan with no surfaced recovery signal.

## Acceptance

- Retention treats each validated receipt and plan as one pair with a recoverable deletion protocol.
- Failure at either removal boundary is observable and cannot silently strand an unowned counterpart.
- Concurrent replacement or content mutation preserves both user-owned files and reports the skipped pair.
- Retention remains bounded, best-effort at its callers, and covered by deterministic fault injection.

## Sub-Tasks

- [ ] Define paired retention outcomes and recovery order
- [ ] Preserve and report partial-removal state safely
- [ ] Add failure-at-each-boundary tests
- [ ] Run bootstrap retention and recovery gates

## Notes

- Evidence: `internal/bootstrap/receipt_retention.go:70-76`.

## Deviations

None.
