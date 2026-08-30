# TASK 411: Reconcile abandoned pending approvals

## Why

`MarkOwnerAbandoned` moves an owned reservation to indeterminate while leaving its pending approval record unchanged. State validation then rejects the transition because a pending approval requires the reservation's original pending status, and the dead owner held the only sealed request token needed for normal finalization.

## Acceptance

- Owner abandonment atomically resolves every pending approval associated with transitioned reservations.
- The terminal approval status and reason are explicit, deterministic, and non-replayable.
- Unrelated approvals and reservations remain unchanged.
- Tests cover pre-call, dispatched, indeterminate, multiple approvals, stale versions, retries, and crash recovery.

## Sub-Tasks

- [x] Map approval/reservation transition invariants and terminalization helpers.
- [x] Terminalize affected approvals in the same locked state transition.
- [x] Add crash-owner and replay regressions.
- [x] Run focused action-state tests.

## Notes

- Verified from finding 89 and its duplicate finding 178.
- Confirmed on current source: a reserved call with an approval-count charge fails in `commitDispatch` because the pending charge is still reserved; without that budget dimension the resulting pending/indeterminate pairing fails state validation.
- `unavailable` is the existing deterministic terminal status for `authority_unavailable`; replay of its sealed request state returns `approval_replayed` with the persisted terminal status.
- The owner transition must release the pending approval charge before committing dispatch capacity, then persist both terminal approval and indeterminate reservation through the existing transaction journal.
- Verified with focused owner-abandonment regressions, the complete `internal/actionstate` suite, and `make test-fast`.

## Deviations
