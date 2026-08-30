# TASK 413: Settle indeterminate results per budget cap

## Why

`IndeterminateCommitted` chooses the maximum reserved result across all charges, then validates that value against every individual reservation. When budgets have different result caps, reconciliation fails as state corruption instead of conservatively settling each budget within its own reservation.

## Acceptance

- Indeterminate committed settlement charges each budget no more than its own reserved result bytes.
- Differing nested/session/run caps reconcile successfully and conservatively.
- Exact known-result settlement and overflow detection remain unchanged.
- Tests cover narrow/wide caps in every order, zero result reservations, counter boundaries, retries, and persisted reload.

## Sub-Tasks

- [x] Define per-charge indeterminate settlement semantics.
- [x] Apply capped usage independently to each charge.
- [x] Add multi-budget and overflow regressions.
- [x] Run focused action-state budget tests.

## Notes

- Verified from finding 91 after correcting its effect: current code does not overbook because `exceedsReservedResult` rejects the maximum; it makes valid multi-cap reconciliation impossible.
- Confirmed on current source and a persisted valid-state regression: the reservation validator permits independent per-charge result ceilings, but `OutcomeIndeterminateCommitted` derives one maximum and applies it to every non-zero charge.
- Known-result outcomes retain their exact shared byte count and cross-charge cap validation. Only unknown committed outcomes consume each charge's already-reserved ceiling.
- Focused indeterminate-result regressions, the complete `internal/actionstate` suite, and `make test-fast` pass.

## Deviations
