# TASK 316: Eliminate logical-condition child allocations

## Why

Every logical condition node allocates a `[]conditionEvaluation` sized to its children before combining results. Deep or broad action predicates create repeated heap churn on the MCP evaluation path.

## Acceptance

- Logical conditions combine child state, completeness, and node counts without a heap slice per node.
- `all`, `any`, invalid kind, depth, node-limit, and validation-only behavior remain byte-for-byte equivalent.
- Benchmarks prove allocation reduction for representative and maximum condition trees.
- Action unit, fuzz, race, and benchmark gates pass.

## Sub-Tasks

- [ ] Define streaming combination invariants
- [ ] Remove the per-node child result slice
- [ ] Add equivalence and allocation tests
- [ ] Run action and gateway benchmarks

## Notes

- Evidence: `internal/action/condition_eval.go:106-128`.

## Deviations

None.
