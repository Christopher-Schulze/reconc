# TASK 423: Persist and expire pending tool correlations

## Why

Session mutation copies the state struct shallowly, so in-place changes to an existing `PendingToolCalls` map can compare equal and skip persistence. Pending Antigravity calls also have no TTL or session-end reaping, so missing or changed post events can permanently consume the bounded correlation capacity.

## Acceptance

- Every pending-call insert and delete persists independently of current map cardinality.
- Retrying a mutation after evidence rotation is idempotent and cannot duplicate, lose, or misassociate a correlation.
- Pending entries have a deterministic session/lifetime bound and cannot permanently taint a project after a lost post event.
- Reaping cannot misassociate a late post event with another call or silently discard live correlations.
- Adversarial tests cover two inserts, a middle delete, last delete, host crash, changed post keys, late delivery, and capacity pressure.

## Sub-Tasks

- [ ] Make mutable session maps copy-on-write at the mutation boundary.
- [ ] Make the post-rotation mutation retry explicitly idempotent for pending-call operations.
- [ ] Define stable correlation identity, expiry, and session-end cleanup semantics.
- [ ] Add persistence and lifecycle regressions with deterministic clocks.
- [ ] Run focused Antigravity and session-state tests.

## Notes

- Verified from findings 102, 189, and 215 plus worker finding 418.
- The first insert (`nil` to map) and last delete (empty map to `nil`) persist today; later inserts and non-terminal deletes are the lost cases.

## Deviations
