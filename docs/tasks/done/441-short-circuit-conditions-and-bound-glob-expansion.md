# TASK 441: Short-circuit conditions and bound glob expansion

## Why

Logical `all`/`any` conditions continue evaluating after their truth value is decisive, and brace-alternative glob expansion can materialize substantial intermediate strings/maps before the byte budget rejects the pattern.

## Acceptance

- Decisive logical conditions stop evaluating unnecessary children without changing decisions, completeness, trace, reason strength, provenance, or operand summaries.
- Glob compilation has explicit alternative/program/count budgets and rejects amplification before excessive allocation.
- Maximum legal plans remain supported and deterministic.
- Benchmarks prove lower work/allocations for decisive 1,024-node conditions and adversarial brace patterns.

## Sub-Tasks

- [x] Specify which condition metadata remains observable after a decisive child.
- [x] Add semantics-preserving short-circuit evaluation.
- [x] Add early glob expansion cost/count admission.
- [x] Run focused action tests and benchmarks.

## Notes

- Verified from findings 173 and 174.
- If complete child metadata is contractually required, the condition optimization must be narrowed rather than changing trace semantics.
- `actual`, `required`, `summary`, completeness, reason strength, and the full compiled node count remain observable. The fast path is therefore restricted to root `exists` predicates on request-owned values, whose metadata is exact without request traversal or operator execution.
- Baseline to optimized decisive 1,024-node condition: about 153 us to 38 us, zero allocations in both. Adversarial brace rejection: about 474 ms and 293 MB to 35 us and 490 B.

## Deviations
