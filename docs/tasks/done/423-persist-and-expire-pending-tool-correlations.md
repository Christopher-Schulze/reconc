# TASK 423: Persist and expire pending tool correlations

## Why

Session mutation copies the state struct shallowly, so in-place changes to an existing `PendingToolCalls` map can compare equal and skip persistence. The current insertion helper already clones that map, but Antigravity post-event deletion does not. Pending calls also have no TTL or session-end reaping, so missing or changed post events can permanently consume the bounded correlation capacity.

## Acceptance

- Every pending-call insert and delete persists independently of current map cardinality.
- Retrying a mutation after evidence rotation is idempotent and cannot duplicate, lose, or misassociate a correlation.
- Pending entries have a deterministic session/lifetime bound and cannot permanently taint a project after a lost post event.
- Reaping cannot misassociate a late post event with another call or silently discard live correlations.
- Adversarial tests cover two inserts, a middle delete, last delete, host crash, changed post keys, late delivery, and capacity pressure.

## Sub-Tasks

- [x] Make mutable session maps copy-on-write at the mutation boundary.
- [x] Make the post-rotation mutation retry explicitly idempotent for pending-call operations.
- [x] Define stable correlation identity, expiry, and session-end cleanup semantics.
- [x] Add persistence and lifecycle regressions with deterministic clocks.
- [x] Run focused Antigravity and session-state tests.

## Notes

- Verified from findings 102, 189, and 215 plus worker finding 418.
- The original finding observed that only the first insert (`nil` to map) and last delete (empty map to `nil`) persisted.
- Reverification found that TASK 391 had since made inserts copy-on-write, so repeated inserts persisted. Antigravity post-event deletion still mutated the loaded map in place and could skip every non-terminal delete.
- Pending-call insertion and removal now clone the map, and the fixed timestamp captured outside the mutation closure makes the evidence-rotation retry idempotent.
- Correlations carry a creation timestamp, expire after 24 hours, reject conflicting reuse of one live host key, and are cleared at Antigravity PostInvocation. Legacy or future-dated records expire fail closed, while bounded tombstones prevent retired keys from being reassigned within the invocation.
- Capacity and identity conflicts deny only the affected PreToolUse event without persisting repository-wide evidence taint. Changed, expired, and unmatched post keys preserve unrelated live calls.
- Adversarial tests cover two persisted inserts, non-terminal and last deletes, rotation retry, host-crash expiry, future timestamps, changed and late posts, unmatched-post tombstones, retired-key reuse, identity conflicts, session cleanup, and live or tombstone capacity pressure.
- Focused Antigravity and session-state tests passed in 0.376s. Formatting, generated-reference validation, and `git diff --check` passed.

## Deviations

- The whole `internal/runtime/agentsession` package run reached the operator's 30-second local ceiling and was not repeated; no failure output was produced. Full, race, release-trust, and platform suites remain deferred to the single queue-end gate run, and no Windows tests were run locally.
