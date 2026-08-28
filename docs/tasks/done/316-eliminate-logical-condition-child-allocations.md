# TASK 316: Eliminate logical-condition child allocations

## Why

Every logical condition node allocates a `[]conditionEvaluation` sized to its children before combining results. Deep or broad action predicates create repeated heap churn on the MCP evaluation path.

## Acceptance

- Logical conditions combine child state, completeness, and node counts without a heap slice per node.
- `all`, `any`, invalid kind, depth, node-limit, and validation-only behavior remain byte-for-byte equivalent.
- Benchmarks prove allocation reduction for representative and maximum condition trees.
- Action unit, fuzz, race, and benchmark gates pass.

## Sub-Tasks

- [x] Define streaming combination invariants
- [x] Remove the per-node child result slice
- [x] Add equivalence and allocation tests
- [x] Run action and gateway benchmarks

## Notes

- Evidence: `internal/action/condition_eval.go:106-128`.
- Logical folds now combine each child result immediately, preserving three-valued state, reason strength, provenance ranking, completeness, and node accounting without retaining a per-node result slice.
- Apple M1 representative (4-way, three nested logical levels) and maximum legal (1023 leaves) benchmarks both measured 0 B/op and 0 allocs/op across three 100-iteration samples.
- Verification: action and MCP gateway unit/race tests, condition metadata and invalid-child regressions, Action Fuzz targets, condition benchmarks, `make test-fast`, `make vet`, Staticcheck v0.8.1, and `make self-host` all passed.

## Deviations

None.
