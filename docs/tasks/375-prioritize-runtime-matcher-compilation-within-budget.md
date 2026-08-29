# TASK 375: Prioritize runtime matcher compilation within budget

## Why

`compileRuntimePathMatchers` spends one global 4 MiB budget in policy order. Patterns after exhaustion retain no compiled matcher and permanently use the materially slower dynamic path even when a different compilation order would cover more high-cost matches within the same memory ceiling.

## Acceptance

- The 4 MiB matcher-memory ceiling remains hard and deterministic.
- Compilation order is based on a documented stable cost/benefit rule rather than incidental policy order.
- Identical plans produce identical compiled/fallback selections and identical matching results.
- Adversarial matcher benchmarks prove reduced fallback cost for over-budget plans without increasing the memory bound.

## Sub-Tasks

- [ ] Add an over-budget matcher benchmark and record compiled-versus-dynamic baselines.
- [ ] Define and implement deterministic budget prioritization.
- [ ] Add equivalence, ordering, and memory-ceiling regressions.
- [ ] Run focused matcher tests and benchmarks.

## Notes

- Verified from finding 2.
- Current `compileRuntimePathMatchers` sets `matcher.glob` to nil once cumulative compiled bytes exceed the shared budget.
- Existing benchmarks show the dynamic path is roughly two to three times slower than the compiled path for the sampled rules.

## Deviations
