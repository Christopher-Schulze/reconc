# TASK 213: Compute action argument size once per evaluation

## Why

Budget evaluation computes expected usage independently for each budget
candidate. When argument-byte limits are present, the same validated request
arguments can be marshaled repeatedly even though their canonical bytes do not
change within the evaluation.

## Acceptance

- Canonical argument bytes or their exact byte length are computed once at the
  validated request boundary and reused by every budget candidate.
- The size matches the canonical JSON representation used by action identity
  and does not depend on map iteration, caller-provided formatting, or a lossy
  estimate.
- Candidates without argument-byte limits do not trigger unnecessary
  serialization.
- Marshal failures remain fail closed and are reported consistently for all
  affected candidates.
- Tests cover multiple budgets, absent limits, Unicode, numbers, nested values,
  and maximum arguments; benchmarks prove one serialization.

## Sub-Tasks

- [x] Define canonical argument-size ownership
- [x] Compute size lazily once per evaluation
- [x] Reuse it across budget candidates
- [x] Add semantic and benchmark tests
- [x] Run action and complete Go gates

## Notes

- The validated, canonical `Request.Arguments` value remains the source of
  truth. `normalizeBudgetInput` first checks whether any selected declaration
  has an `argument_bytes` limit; only then does it marshal the canonical value
  once and pass the exact byte length to every candidate validator. Budgets
  without that dimension retain the no-serialization path.
- `expectedBudgetUsage` remains the public single-budget API and serializes
  only when no shared evaluation size is supplied. Shared errors, absent
  arguments, Unicode, normalized numbers, nested values, and a corrupt value
  all fail closed consistently. Focused semantic tests and the shared-size
  benchmark pass; the complete repository gate remains for queue completion.

## Deviations

None.
