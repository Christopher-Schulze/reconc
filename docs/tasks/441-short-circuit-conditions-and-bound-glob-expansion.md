# TASK 441: Short-circuit conditions and bound glob expansion

## Why

Logical `all`/`any` conditions continue evaluating after their truth value is decisive, and brace-alternative glob expansion can materialize substantial intermediate strings/maps before the byte budget rejects the pattern.

## Acceptance

- Decisive logical conditions stop evaluating unnecessary children without changing decisions, completeness, trace, reason strength, provenance, or operand summaries.
- Glob compilation has explicit alternative/program/count budgets and rejects amplification before excessive allocation.
- Maximum legal plans remain supported and deterministic.
- Benchmarks prove lower work/allocations for decisive 1,024-node conditions and adversarial brace patterns.

## Sub-Tasks

- [ ] Specify which condition metadata remains observable after a decisive child.
- [ ] Add semantics-preserving short-circuit evaluation.
- [ ] Add early glob expansion cost/count admission.
- [ ] Run focused action tests and benchmarks.

## Notes

- Verified from findings 173 and 174.
- If complete child metadata is contractually required, the condition optimization must be narrowed rather than changing trace semantics.

## Deviations
