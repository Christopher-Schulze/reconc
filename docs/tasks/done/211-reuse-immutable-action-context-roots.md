# TASK 211: Reuse immutable action context roots

## Why

Action predicate evaluation rebuilds the same root `Value` object from the
normalized request/context for each root-based predicate. Object construction
sorts and validates members, so a plan with many predicates repeats allocations
and canonicalization for immutable evaluation input.

## Acceptance

- Evaluation constructs each supported root object once from validated,
  deep-owned input and reuses it read-only across predicates.
- Pointer presence, null versus missing, provenance, availability, taint,
  credential labels, and repository-effect fields remain identical.
- `exists` may use a faster path only if it returns the same state, reason,
  provenance, and trace metadata as full resolution.
- No mutable request slice/map can change a cached root after construction.
- Differential and race tests cover all pointer roots and predicate kinds;
  benchmarks cover high predicate counts.

## Sub-Tasks

- [x] Inventory action root-object inputs and ownership
- [x] Build immutable roots once per evaluation
- [x] Reuse roots across pointer and predicate evaluation
- [x] Add differential, mutation, race, and benchmark tests
- [x] Run action and complete Go gates

## Notes

- The current implementation materializes the context root in
  `selectContextRoot`; arguments, result, and progress already arrive as
  canonical `Value` roots and therefore require no second object build. The
  normalized request owns a deep clone of every context value. An
  evaluation-local `predicateRoots` now caches the selected context-root
  result, including pointer state, provenance, availability, and the exact
  operand summary. Context-member pointers continue to use binary search and
  never pay for root materialization.
- `evaluateConditionTreeWithRoots` is used only by the production
  accumulator. The existing checked wrappers remain for direct callers and
  malformed compiled-predicate tests, while the immutable compiled plan
  path skips duplicate pointer validation in the hot loop. Two predicates
  reuse one root in tests, request-slice mutation cannot alter the cached
  result, all four source roots are compared against the checked path, and a
  128-predicate benchmark records the high-cardinality path.
- Focused action tests pass. Fresh final candidate verification in TASK 221
  passed the complete root and portable-template race gates.

## Deviations

None.
