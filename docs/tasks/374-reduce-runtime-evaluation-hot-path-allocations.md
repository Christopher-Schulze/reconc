# TASK 374: Reduce runtime evaluation hot-path allocations

## Why

Filtered and pre-command evaluations still allocate transient rule-index, assurance-gate, command, and empty collector slices on every check. Composite command rules are also evaluated before the pre-command path proves that their forbidden command can trigger.

## Acceptance

- Filtered and pre-command evaluation avoid per-call index bitsets and unnecessary rule slices.
- Assurance rules reuse immutable normalized command expectations from the runtime plan without cloning nested command lists.
- Empty stable collectors allocate no map until the first retained value.
- Benchmarks cover mixed filtered, pre-command, and assurance-heavy plans and prove lower allocations without changing decisions or traces.

## Sub-Tasks

- [ ] Measure current filtered, pre-command, assurance, and empty-collector allocation baselines.
- [ ] Reuse plan-owned immutable data and preflight composite command triggers before rule evaluation.
- [ ] Add decision, trace, ordering, and immutability regressions.
- [ ] Run focused runtime tests and benchmarks.

## Notes

- Verified from OMP session `01a04db2-0312-77cb-b45b-a22a578cd0d2`, findings 1, 6, 8, and 20.
- `assuranceGatesFromRule` copies gate and command slices; `plan.indexesFor` allocates a boolean selection plus output indexes; `newStableStringCollector` eagerly creates its map.
- TASK 344 cached normalized command expectations and TASK 345 removed full-plan rule slices, but these filtered/pre-command allocation paths remain.

## Deviations
