# TASK 570: Retry safe MCP dispatch state conflicts

## Why

Finding F5: another budgeted call can advance the global state digest between
Reserve and MarkDispatched. The first call then blocks despite a valid reservation.
Reserve already has bounded typed conflict recovery; the approval-free dispatch
boundary lacks equivalent recovery.

## Acceptance

- A deterministic competing reservation between reserve and dispatch does not
  block an otherwise permitted approval-free call with sufficient budgets.
- The retry observes current state and reevaluates state-bound policy/budget inputs.
  Real policy/identity/window changes and exhausted limits remain fail-closed.
- Dispatch happens at most once, counters charge exactly once, and reservations
  settle/release without leaks after successes, cancellation, and retry exhaustion.
- Approval-bound calls retain their exact authorization state and do not inherit
  an approval across a changed state version.
- Targeted budget/gateway concurrency tests, the root suite, vet, lint, and development
  build pass; documentation describes bounded pre-dispatch conflict recovery.
- The completed TASK is archived, committed, and pushed to origin/main.

## Sub-Tasks

- [ ] Reproduce the reserve/dispatch interleaving with real action state and a deterministic barrier.
- [ ] Expose only genuine transition-version conflicts as the existing typed sentinel.
- [ ] Refresh and reevaluate the approval-free state-bound input before bounded dispatch retry.
- [ ] Cover exhaustion, stale identity/window, approval fencing, accounting, and cancellation; flush docs.
- [ ] Run validation, review all changes, archive, commit, and push.

## Technical Plan

Use ErrStateVersionChanged only for the digest mismatch in transitionState;
do not classify other ReasonStateUnavailable failures as retryable. At the
approval-free pre-dispatch boundary, reuse the existing reservation identity and
Reserve's same-call retry snapshot to validate governing generation, exact charges,
current budgets, repository/server/context identities, and fixed windows. Rebuild
state-version/budget-dependent evaluator input and its identity snapshot, then
record the fresh decision before MarkDispatched. Preserve observed evidence only
under its existing identity boundary; changed decisions cannot silently bypass
approval or block requirements.

Bound recovery by the existing MaxReservationConflictRetries and caller context.
Never retry the upstream operation after dispatch. Do not remove CAS, serialize
all calls under a global lock, or introduce a per-reservation state engine. Reuse
existing ledger and release/terminal accounting paths and inspect their retry
behavior for this specific boundary.

## Notes

Source owners: internal/mcpgateway/call.go, request.go,
internal/actionstate/budget_transition.go, budget_store.go.
Existing pre-dispatch concurrency coverage uses no budgets; reservation-conflict
coverage mutates before Reserve, not between Reserve and MarkDispatched.

## Deviations

None.
