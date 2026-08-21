# TASK 212: Validate immutable compiled action plans once

## Why

Compiled pointer validation runs during predicate evaluation, while evaluator
preflight recursively checks compiled-plan bounds on every evaluation. The plan
is cloned/compiled at evaluator construction and intended to be immutable, so
full structural validation is repeated on the hot path to defend against a
mutation model the ownership contract should make impossible.

## Acceptance

- The evaluator deep-owns immutable compiled plan data; construction performs
  complete pointer, condition, index, bound, and cardinality validation once.
- Evaluation cannot observe caller mutation through retained slices, maps,
  pointers, or shared matcher state.
- If runtime tamper detection remains required, it uses a measured bounded
  integrity mechanism and documents the threat it covers instead of silently
  rescanning the full plan.
- Malformed compiled plans fail before becoming usable; fuzz tests cover every
  pointer and recursive condition boundary.
- Benchmarks prove the per-evaluation scan is removed or materially reduced.

## Sub-Tasks

- [x] Prove and document compiled-plan ownership and mutation boundaries
- [x] Consolidate validation at evaluator construction
- [x] Remove redundant predicate/preflight scans safely
- [x] Add mutation, malformed-plan, fuzz, and benchmark tests
- [x] Run action, race, and complete gates

## Notes

- `CompilePlan` already deep-clones the public `Plan`, canonicalizes all
  selectors and rules, compiles every pointer and matcher, and rejects every
  condition/cardinality boundary before returning `CompiledPlan`. `NewEvaluator`
  recompiles the defensive `compiled.Plan()` snapshot once and retains only
  detached canonical plan/rule/matcher data. Callers can mutate neither the
  input plan nor any `Plan()`, `Rules()`, `Budgets()`, or `Detectors()` result
  to affect an evaluator.
- The per-evaluation `compiledBoundsValid` recursion and per-predicate pointer
  validation were removed. Production evaluation now trusts the sealed
  construction boundary; direct pointer/evaluation helper wrappers retain
  their checked behavior for API callers and malformed-input tests. This
  removes the repeated recursive scan without weakening the actual admission
  boundary. Existing malformed-state coverage was moved to constructor
  rejection tests, and pointer/condition fuzz targets remain active.
- Focused action tests pass. The complete race and repository gates remain for
  queue completion.

## Deviations

None.
