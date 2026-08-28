# TASK 292: Narrow MCP pre-dispatch serialization and retry policy

## Why

The gateway holds `transitionMu` across policy loading, evidence inspection, ledger I/O, and reservation. Optimistic state-version conflicts then repeat most of that work, and the retry limit is coupled to the unrelated concurrent-call capacity.

## Acceptance

- `transitionMu` covers only the state transitions that require gateway-local linearization.
- Policy, inspection, and ledger I/O execute outside the global pre-dispatch mutex unless a measured invariant requires otherwise.
- State-version retries use a dedicated documented bound and recompute only state-version-bound inputs.
- Concurrency tests prove independent calls progress in parallel without weakening reservation or approval ordering.

## Sub-Tasks

- [ ] Map every pre-dispatch value to its mutation and freshness boundary
- [ ] Introduce a dedicated reservation-conflict retry policy
- [ ] Narrow the transition critical section
- [ ] Add contention, mutation, race, and benchmark coverage

## Notes

- Evidence: `internal/mcpgateway/call.go:106-199`. TASK 273 removed the former `stateMu`, but the current critical section still encloses filesystem and ledger work.

## Deviations

None.
