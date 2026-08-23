# TASK 273: Decouple MCP gateway state and progress pipelines

## Why

The gateway's global `stateMu` currently surrounds policy loading, filesystem
inspection, ledger writes, budget reserve/settle operations, and post-result
normalization. This serializes most of the call pipeline even though the public
limit allows four concurrent calls. Downstream progress is also evaluated
synchronously inside the transport reader path, so one filesystem-heavy
progress event can stall unrelated responses and notifications.

## Acceptance

- The gateway mutex protects only in-memory state that truly requires global
  linearization. Policy snapshots, inspection, ledger I/O, diagnostics, and
  independent call work execute outside it.
- Budget reservation, approval transitions, dispatch, settlement, failure, and
  indeterminate recovery remain linearizable across concurrent calls and
  multiple processes through the action-state/ledger owners.
- Progress enters a bounded per-call queue with explicit count/byte/time
  budgets. Queue saturation, cancellation, worker failure, and shutdown fail
  closed according to the existing progress disposition contract.
- Progress decisions are processed and forwarded in source order. Final tool
  results cannot overtake admitted progress; call completion drains or cancels
  the queue deterministically before terminal ledger/state publication.
- No goroutine or queue survives call completion, timeout, downstream failure,
  session shutdown, or upstream disconnect.
- Diagnostics never perform blocking output while holding global gateway state.
- Concurrency tests prove two independent calls make progress simultaneously,
  reservation races remain safe, progress cannot reorder with results, and a
  slow sink cannot stall the transport reader.
- End-to-end and contention benchmarks record throughput/latency for one and
  four calls, slow progress, and ledger/state contention.
- Gateway docs, race/leak tests, publication gates, and complete verification
  pass.

## Sub-Tasks

- [~] Map every `stateMu` invariant and assign it to the narrow owning component
- [ ] Move policy, inspection, ledger, and diagnostic I/O outside the global critical section
- [ ] Preserve atomic budget and approval transitions through their existing stores
- [ ] Design the bounded ordered per-call progress queue and completion barrier
- [ ] Implement lifecycle-owned progress workers and deterministic shutdown
- [ ] Add concurrent-call, slow-sink, saturation, ordering, cancellation, and leak tests
- [ ] Add contention and end-to-end benchmarks plus calibrated evidence
- [ ] Update MCP concurrency, progress, and failure-semantics documentation
- [ ] Run race, leak, protocol, publication, and complete repository gates

## Notes

- Current evidence: `internal/mcpgateway/call.go` locks around `prepareCall`,
  which performs snapshot freshness, action inspection, ledger append, state
  reserve, and evaluation. `result.go` similarly locks around `finishCall`.
- Current evidence: `sdkDownstream.routeProgress` invokes the registered sink
  synchronously; `handleProgress` may load evidence, inspect, evaluate, write
  ledger records, and notify upstream.
- Negative caching of slow failure results is out of scope. Failure decisions
  are intentionally non-cacheable; changing that contract would trade
  performance for stale security decisions.
- The queue must not acknowledge/drop progress before its security decision is
  ordered relative to the final result. A fire-and-forget goroutine is not an
  acceptable implementation.

## Deviations

None.
