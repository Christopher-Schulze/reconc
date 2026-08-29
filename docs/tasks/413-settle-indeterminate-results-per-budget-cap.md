# TASK 413: Settle indeterminate results per budget cap

## Why

`IndeterminateCommitted` chooses the maximum reserved result across all charges, then validates that value against every individual reservation. When budgets have different result caps, reconciliation fails as state corruption instead of conservatively settling each budget within its own reservation.

## Acceptance

- Indeterminate committed settlement charges each budget no more than its own reserved result bytes.
- Differing nested/session/run caps reconcile successfully and conservatively.
- Exact known-result settlement and overflow detection remain unchanged.
- Tests cover narrow/wide caps in every order, zero result reservations, counter boundaries, retries, and persisted reload.

## Sub-Tasks

- [ ] Define per-charge indeterminate settlement semantics.
- [ ] Apply capped usage independently to each charge.
- [ ] Add multi-budget and overflow regressions.
- [ ] Run focused action-state budget tests.

## Notes

- Verified from finding 91 after correcting its effect: current code does not overbook because `exceedsReservedResult` rejects the maximum; it makes valid multi-cap reconciliation impossible.

## Deviations
