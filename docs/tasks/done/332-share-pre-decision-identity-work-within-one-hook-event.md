# TASK 332: Share pre-decision identity work within one hook event

## Why

Pre-decision key construction loads and hashes policy sources, lock bytes, session state, payload, and Git aliases. One hook event can compute that full key two or three times around cache read and evaluation.

## Acceptance

- One request-scoped capture shares immutable payload, policy-source, lock, session, and alias work where observations are identical.
- Required pre/post mutation sampling remains separate and compares typed component identities rather than blindly recomputing every expensive component.
- A change to any bound input invalidates reuse and evaluation remains fail closed.
- Cache-hit, miss, alias mutation, source mutation, and process-count benchmarks pass.

## Sub-Tasks

- [x] Decompose the key into immutable and resampled components
- [x] Build one request-scoped identity capture
- [x] Preserve exact pre/post mutation detection
- [x] Add read-count, process-count, mutation, and race tests

## Notes

- Evidence: `internal/runtime/agentsession/pre_decision_cache.go:38-52,76-146,196-214`. TASK 244 intentionally bound these inputs but did not eliminate repeated full construction.
- `preDecisionIdentity` now stores the payload, lock, source, session, taint,
  and alias identities as typed comparable components. The decoded payload
  identity is captured once per hook event; cache validation and post-decision
  checks resample mutable components and compare the typed record before using
  a key.
- Cache reads and writes preserve the existing fail-closed behavior for missing
  IDs, unreadable inputs, malformed entries, policy/evidence mutation, alias
  mutation, and concurrent changes. The initial alias snapshot remains the one
  used for shell analysis; a fresh alias snapshot is required before caching.
- Added mutation and concurrent-resampling regressions. The existing Git
  process-count check remains green, and the identity benchmark reports lower
  work for typed resampling than full key reconstruction. The complete
  `internal/runtime/agentsession` test suite and focused race suite are green.

## Deviations

None.
