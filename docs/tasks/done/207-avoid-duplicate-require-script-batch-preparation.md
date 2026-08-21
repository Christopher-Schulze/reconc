# TASK 207: Avoid duplicate require-script batch preparation

## Why

`evaluateBatchedRequireScripts` computes scope and match contexts for candidate
rules before it knows whether at least two compatible rules form a batch. A
singleton or ineligible group falls back to normal evaluation, which recomputes
the same preparation.

## Acceptance

- Cheap, semantics-preserving eligibility and grouping occur before expensive
  scope/context preparation, or preparation results are reused by fallback.
- Batched and fallback paths produce identical violation order, matched paths,
  evidence provenance, read budgets, errors, and recommendations.
- Singleton, mixed-scope, templated, missing-file, and partial-error groups are
  covered explicitly.
- No group is batched if doing so changes short-circuit, fail-closed, or budget
  behavior.
- Benchmarks show no duplicate preparation for fallback candidates and no
  regression for genuine batches.

## Sub-Tasks

- [x] Define cheap require-script batch eligibility
- [x] Reorder grouping or share preparation with fallback
- [x] Preserve evidence and error ordering
- [x] Add differential and benchmark tests
- [ ] Run runtime and complete gates

## Notes

- `evaluateBatchedRequireScripts` now groups candidates by immutable script and
  timeout key before scope matching or template-context collection. Singleton
  candidates therefore fall directly through to the normal evaluator without
  duplicate preparation; groups that lose candidates to scope/context misses
  remain unhandled and preserve the established fail-closed fallback.
- Existing batch, scope-miss, timeout-fallback, templated, and mixed-result tests
  remain green. A singleton benchmark exercises the no-batch path and reports
  allocations; a full runtime gate remains for the final queue completion.

## Deviations

None.
