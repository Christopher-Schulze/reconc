# TASK 375: Prioritize runtime matcher compilation within budget

## Why

`compileRuntimePathMatchers` spends one global 4 MiB budget in lexical pattern order. Patterns that do not fit retain no compiled matcher and permanently use the materially slower dynamic path even when a cost/benefit order would cover more high-use matches within the same memory ceiling.

## Acceptance

- The 4 MiB matcher-memory ceiling remains hard and deterministic.
- Compilation order is based on a documented stable cost/benefit rule rather than incidental policy order.
- Identical plans produce identical compiled/fallback selections and identical matching results.
- Adversarial matcher benchmarks prove reduced fallback cost for over-budget plans without increasing the memory bound.

## Sub-Tasks

- [x] Add an over-budget matcher benchmark and record compiled-versus-dynamic baselines.
- [x] Define and implement deterministic budget prioritization.
- [x] Add equivalence, ordering, and memory-ceiling regressions.
- [x] Run focused matcher tests and benchmarks.

## Notes

- Verified from finding 2.
- Reality check corrected the finding's ordering detail: current code sorts unique patterns lexically, then applies first-fit admission. A matcher that does not fit falls back, while a later smaller matcher may still compile; selection is nevertheless driven by pattern identity rather than expected reuse.
- Existing benchmarks show the dynamic path is roughly two to three times slower than the compiled path for the sampled rules.
- The stable priority is exact policy-reference count per logical compiled byte; ties prefer more references, then fewer bytes, then lexical pattern identity. Selection is independent of rule order.
- Adversarial 48-cold/8-hot plan baseline (three runs): 7 hot fallbacks, 7 allocs/op, 168 B/op, 8,090-8,165 ns/op. Prioritized result: 0 hot fallbacks, 0 allocs/op, 0 B/op, 709-744 ns/op while retaining at most 4 MiB of compiled programs.

## Deviations
