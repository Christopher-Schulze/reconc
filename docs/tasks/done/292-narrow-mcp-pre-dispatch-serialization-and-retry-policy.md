# TASK 292: Narrow MCP pre-dispatch serialization and retry policy

## Why

The gateway holds `transitionMu` across policy loading, evidence inspection, ledger I/O, and reservation. Optimistic state-version conflicts then repeat most of that work, and the retry limit is coupled to the unrelated concurrent-call capacity.

## Acceptance

- `transitionMu` covers only the state transitions that require gateway-local linearization.
- Policy, inspection, and ledger I/O execute outside the global pre-dispatch mutex unless a measured invariant requires otherwise.
- State-version retries use a dedicated documented bound and recompute only state-version-bound inputs.
- Concurrency tests prove independent calls progress in parallel without weakening reservation or approval ordering.

## Sub-Tasks

- [x] Map every pre-dispatch value to its mutation and freshness boundary
- [x] Introduce a dedicated reservation-conflict retry policy
- [x] Narrow the transition critical section
- [x] Add contention, mutation, race, and benchmark coverage

## Notes

- Evidence: `internal/mcpgateway/call.go:106-199`. TASK 273 removed the former `stateMu`, but the current critical section still encloses filesystem and ledger work.
- Boundary map: the policy snapshot, inspector, normalized request body,
  selected tool, repository/taint evidence, and inspection evidence are stable
  per call. Only `Request.StateVersion`, the atomic reservation attempt, and the
  resulting budget/evaluator identity snapshot belong to the optimistic
  action-state boundary. Ledger creation starts only after reservation succeeds.
- `transitionMu` remains on approval consume/finalize paths whose gateway-local
  ordering spans pending-state ownership. Durable reservation ordering remains
  owned by the multi-process action-state store.
- Parallel approval issuance exposed the real cross-transition invariant: an
  independent reservation may advance global state after a call reserves but
  before it requests approval. The store now rebinds that still-live owned
  reservation and re-evaluates it against the current state while holding the
  durable state lock instead of requiring a broad gateway mutex.
- The dedicated conflict budget is eight retries, independent of the four-call
  admission capacity. The focused contention benchmark measured 12,897,800
  ns/op at concurrency one and 5,359,469 ns/op at concurrency four on Apple M1
  with a controlled 10 ms evidence delay.
- Verification passed the focused contention and state-mutation regressions,
  the pending-approval capacity regression five consecutive times, MCP and
  action-state race suites, the complete uncached repository race gate, Vet,
  Staticcheck, publication audit, portable harness tests, and release trust.

## Deviations

None.
