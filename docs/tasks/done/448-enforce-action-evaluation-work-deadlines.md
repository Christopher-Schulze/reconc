# TASK 448: Enforce action evaluation work deadlines

## Why

The MCP gateway measures `EvaluationTimeout` only after synchronous evaluation has finished. Expensive maximum plans can consume all work first and merely have their result replaced afterward.

## Acceptance

- The public evaluation deadline either bounds actual work or is renamed/documented as a post-work latency classification with no false cancellation claim.
- Cancellation checks do not change deterministic decisions, trace order, or fail-closed behavior.
- Maximum legal plans and hostile payload shapes remain resource-bounded.
- Benchmarks and deterministic clock/work tests cover early cancellation, exact deadline, completion races, and normal fast paths.

## Sub-Tasks

- [x] Map cancellable boundaries in selector, condition, inspection, and trace evaluation.
- [x] Thread a low-overhead deadline signal or correct the contract explicitly.
- [x] Add deadline and equivalence regressions.
- [x] Run focused gateway/action tests and benchmarks.

## Notes

- Verified from finding 185 in `internal/mcpgateway/request.go`.
- The old timer started after `Prepare` and could only replace a completed result. The gateway now derives a 500 ms child context before preparation and passes it through preparation, evaluation, and the final cache-publication check.
- Cooperative checks cover bounded batches of at most eight rule selectors or list candidates, every condition node and skipped-condition metadata step, result publication boundaries, and every 64 glob work units. Regex and input cloning remain non-preemptible only within their existing linear, byte-capped primitive.
- Deterministic injected work checkpoints prove early cancellation, deadline precedence at the exact final checkpoint, completion immediately before that checkpoint, uncancellable equivalence, maximum-plan cancellation, and hostile glob interruption.
- Apple M1 benchmarks measured the 64-rule prepared fast path at approximately 63-64 microseconds with a deadline versus 59-60 microseconds without one; the signal added one allocation and about 82 bytes. A full 4,096-rule prepared plan took approximately 0.38-0.67 ms, while deterministic cancellation after two checkpoints returned in approximately 23-25 microseconds.

## Deviations
