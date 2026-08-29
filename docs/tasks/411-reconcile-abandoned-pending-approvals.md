# TASK 411: Reconcile abandoned pending approvals

## Why

`MarkOwnerAbandoned` moves an owned reservation to indeterminate while leaving its pending approval record unchanged. State validation then rejects the transition because a pending approval requires the reservation's original pending status, and the dead owner held the only sealed request token needed for normal finalization.

## Acceptance

- Owner abandonment atomically resolves every pending approval associated with transitioned reservations.
- The terminal approval status and reason are explicit, deterministic, and non-replayable.
- Unrelated approvals and reservations remain unchanged.
- Tests cover pre-call, dispatched, indeterminate, multiple approvals, stale versions, retries, and crash recovery.

## Sub-Tasks

- [ ] Map approval/reservation transition invariants and terminalization helpers.
- [ ] Terminalize affected approvals in the same locked state transition.
- [ ] Add crash-owner and replay regressions.
- [ ] Run focused action-state tests.

## Notes

- Verified from finding 89 and its duplicate finding 178.
- The current abandonment write fails validation rather than merely delaying cleanup until TTL.

## Deviations
