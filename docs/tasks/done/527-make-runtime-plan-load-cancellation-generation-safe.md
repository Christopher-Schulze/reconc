# TASK 527: Make runtime-plan load cancellation generation-safe

## Why

Runtime-plan loading shares one in-flight compilation per repository root and
cancels its worker when the last caller leaves. The last caller decrements the
reference count under lock, releases the lock, and only then cancels the worker.
A new live caller can join that still-registered generation in the gap and
receive its inevitable `context.Canceled` result. In addition, cancellation
during freshness observation removes an already validated cached plan, causing
avoidable cold recompilation for the next caller.

This task consolidates audit candidates C227 and C228. The repair must preserve
bounded cross-root concurrency, same-root deduplication, caller-independent
worker ownership, and source-freshness integrity while making generation
handoff and cache invalidation exact.

## Acceptance

- Give each in-flight root load an explicit lifecycle that distinguishes
  joinable, draining/cancelled, and finished generations under the evaluator
  mutex.
- When the last caller leaves, make that generation non-joinable before or in
  the same critical section that transfers cancellation to its worker.
- A new caller arriving after the generation becomes non-joinable must join or
  start a fresh generation; it must never inherit cancellation from callers that
  have already left.
- Preserve shared work when at least one caller remains. Cancelling an owner or
  waiter must not stop a load still needed by another live caller.
- Ensure an old worker cannot delete, publish over, signal, or otherwise mutate
  a newer generation registered for the same root.
- Preserve the maximum four concurrent root loads, release every slot exactly
  once, close each generation's completion signal exactly once, and leave no
  goroutine or load-map entry after normal completion or cancellation.
- Do not invalidate a previously validated cached plan merely because the
  observing caller was cancelled or its deadline expired. Retain it unless
  lock bytes, source identity, freshness, decoding, or another persistent fact
  proves it stale or invalid.
- Never publish a partially compiled or cancellation-observed plan. A cancelled
  replacement load must leave either the prior valid plan or no plan according
  to proven source state.
- Keep errors attributable to the requesting caller or load generation; do not
  wrap caller cancellation as a lockfile-refresh requirement.
- Use deterministic synchronization hooks/channels in tests. Do not depend on
  timing sleeps to reproduce the last-caller/new-caller race.
- Add regressions for last-caller cancellation followed by an immediate join,
  owner cancellation with surviving waiter, waiter cancellation with surviving
  owner, slot contention, old/new generation overlap, cached freshness
  cancellation, source mutation, and evaluator teardown.
- Do not add panic recovery solely to mask a production panic. Ensure ordinary
  cleanup is structured with deferred ownership where useful, while preserving
  the process's existing panic policy.
- Pass repeated and race-enabled runtime tests plus all required repository
  gates, with no load, slot, plan, or goroutine leaks.

## Sub-Tasks

- [x] Read the complete evaluator plan-cache and in-flight-load state machine, freshness observers, invalidation callers, slot ownership, error wrapping, and prior TASK 505/516 regressions.
- [x] Specify the mutex-protected generation lifecycle and the exact linearization points for join, leave, cancel, finish, delete, and publish.
- [x] Make last-caller cancellation remove or mark the generation non-joinable atomically, preserving safe old-worker cleanup when a replacement generation exists.
- [x] Separate caller cancellation/deadline from persistent freshness failure so a valid cached plan survives transient observation aborts.
- [x] Add deterministic generation-handoff, cache-preservation, source-drift, slot-bound, cancellation, and leak regressions.
- [x] Run high-count and race-enabled focused tests, inspect allocations and goroutines, and verify no test relies on scheduler luck.
- [x] Update runtime cache/cancellation architecture documentation with the final lifecycle and invalidation reasons.
- [x] Run all required repository gates and re-read every modified file before archival.

## Notes

### Observed behavior

- Join used to accept any registered root load. Last-caller decrement then
  unlocked before `cancel()`, so a new caller could inherit `context.Canceled`.
- Cached freshness errors, including caller abort, used to drop a validated plan.
- An old cancelled worker could `invalidateRuntimePlan` without a generation check.

### Implementation

- Each in-flight load has `joinable`, `finished`, and `gen`. Join only if
  `active != nil && active.joinable`. Last caller sets `joinable=false` and
  deletes its map entry in the same critical section, then cancels after unlock.
- Cache hits and compile failures call `runtimePlanCallerAbort` before
  `invalidateRuntimePlanFrom(root, gen)`. Invalidation and publication refuse
  to clobber a newer `entry.gen`.
- `loadFreshRuntimePlanWithContext` returns caller abort unwrapped.
- Deterministic hooks: `joinHook`, `drainHook`, `cacheObserveHook`, `slotWaitHook`.

### Verification

- Focused `go test -race -count=20` on cancellation, handoff, deadline, and
  slot-waiter tests passed. `make test-fast`, `make vet`, `make lint`,
  `git diff --check`, and `make test` (uncached race plus release-trust) passed
  after removing the unused force-invalidate wrapper.

## Deviations

None.
