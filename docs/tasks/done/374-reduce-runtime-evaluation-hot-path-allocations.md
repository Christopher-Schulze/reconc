# TASK 374: Reduce runtime evaluation hot-path allocations

## Why

Filtered and pre-command evaluations still allocate transient rule-index, assurance-gate, command, and empty collector slices on every check. Composite command rules are also evaluated before the pre-command path proves that their forbidden command can trigger.

## Acceptance

- Filtered and pre-command evaluation avoid per-call index bitsets and unnecessary rule slices.
- Assurance rules reuse immutable normalized command expectations from the runtime plan without cloning nested command lists.
- Empty stable collectors allocate no map until the first retained value.
- Benchmarks cover mixed filtered, pre-command, and assurance-heavy plans and prove lower allocations without changing decisions or traces.

## Sub-Tasks

- [x] Measure current filtered, pre-command, assurance, and empty-collector allocation baselines.
- [x] Reuse plan-owned immutable data and preflight composite command triggers before rule evaluation.
- [x] Add decision, trace, ordering, and immutability regressions.
- [x] Run focused runtime tests and benchmarks.

## Notes

- Verified from OMP session `01a04db2-0312-77cb-b45b-a22a578cd0d2`, findings 1, 6, 8, and 20.
- `assuranceGatesFromRule` copies gate and command slices; `plan.indexesFor` allocates a boolean selection plus output indexes; `newStableStringCollector` eagerly creates its map.
- TASK 344 cached normalized command expectations and TASK 345 removed full-plan rule slices, but these filtered/pre-command allocation paths remain.
- Baseline (three runs): pre-command subset 155,616 B/op and 539 allocs/op; irrelevant forbid-command subset 286,914-287,280 B/op and 1,599-1,600 allocs/op; stable collector sizes 128-8192 used 17,976-1,359,281 B/op and 21-98 allocs/op.
- Exact old-commit baselines (three runs, 256-rule mixed filter / 128 assurance gates / empty collector): 2,296 B and 9 allocs/op; 69,632 B and 129 allocs/op; 48 B and 1 alloc/op.
- Optimized benchmarks (three runs): mixed filter 1,024 B and 1 alloc/op; single-kind/all-kind filters 0 B and 0 allocs/op; prepared 128-gate assurance lookup 0 B and 0 allocs/op; empty collector 0 B and 0 allocs/op. Existing end-to-end pre-command benchmarks remained at 539 and 1,599 allocs/op because their selected top-level rule slices were already plan-owned; the new adversarial regression proves irrelevant composites no longer execute side-effecting sub-checks.

## Deviations
