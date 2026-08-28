# TASK 332: Share pre-decision identity work within one hook event

## Why

Pre-decision key construction loads and hashes policy sources, lock bytes, session state, payload, and Git aliases. One hook event can compute that full key two or three times around cache read and evaluation.

## Acceptance

- One request-scoped capture shares immutable payload, policy-source, lock, session, and alias work where observations are identical.
- Required pre/post mutation sampling remains separate and compares typed component identities rather than blindly recomputing every expensive component.
- A change to any bound input invalidates reuse and evaluation remains fail closed.
- Cache-hit, miss, alias mutation, source mutation, and process-count benchmarks pass.

## Sub-Tasks

- [ ] Decompose the key into immutable and resampled components
- [ ] Build one request-scoped identity capture
- [ ] Preserve exact pre/post mutation detection
- [ ] Add read-count, process-count, mutation, and race tests

## Notes

- Evidence: `internal/runtime/agentsession/pre_decision_cache.go:38-52,76-146,196-214`. TASK 244 intentionally bound these inputs but did not eliminate repeated full construction.

## Deviations

None.
