# TASK 448: Enforce action evaluation work deadlines

## Why

The MCP gateway measures `EvaluationTimeout` only after synchronous evaluation has finished. Expensive maximum plans can consume all work first and merely have their result replaced afterward.

## Acceptance

- The public evaluation deadline either bounds actual work or is renamed/documented as a post-work latency classification with no false cancellation claim.
- Cancellation checks do not change deterministic decisions, trace order, or fail-closed behavior.
- Maximum legal plans and hostile payload shapes remain resource-bounded.
- Benchmarks and deterministic clock/work tests cover early cancellation, exact deadline, completion races, and normal fast paths.

## Sub-Tasks

- [ ] Map cancellable boundaries in selector, condition, inspection, and trace evaluation.
- [ ] Thread a low-overhead deadline signal or correct the contract explicitly.
- [ ] Add deadline and equivalence regressions.
- [ ] Run focused gateway/action tests and benchmarks.

## Notes

- Verified from finding 185 in `internal/mcpgateway/request.go`.

## Deviations
