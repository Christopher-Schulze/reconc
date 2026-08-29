# TASK 391: Harden session-state persistence boundaries

## Why

Per-category pending-call limits can combine into a serialized session larger than the hard 1 MiB state ceiling. Passive observation skips the canonical file entirely when canonical and legacy paths are equal, and legacy migration accepts a state with no stored session identity.

## Acceptance

- Mutations enforce an aggregate serialized-state budget before publication and produce durable overflow evidence instead of an unrecoverable save error.
- Passive observation checks the canonical path exactly once when canonical and legacy paths coincide.
- Legacy state without an exact non-empty `session_id` is rejected or quarantined without adoption.
- Completion input derivation never mutates the source `SessionState` through a returned epoch-map alias.
- Adversarial tests cover aggregate maximums, UUID paths, empty legacy identity, collision-shaped legacy names, and no-partial-write behavior.

## Sub-Tasks

- [ ] Define aggregate retained-byte accounting for pending calls and all state fields.
- [ ] Correct canonical/legacy observation iteration.
- [ ] Tighten legacy identity migration and add adversarial fixtures.
- [ ] Make epoch-key relativization return independently owned state on every path used by completion capture.
- [ ] Run focused agent-session tests.

## Notes

- Verified from findings 40, 41, and 42 plus worker finding 412.
- `observeSessionStateResolved` currently skips both iterations when the two paths are equal; `loadSessionStateResolved` accepts an empty legacy `SessionID` before restamping it.
- `RelativizeEpochKeys` returns its input map for empty input or root-resolution failure; `completionExecutionInputs` then inserts Git-derived epochs and can contaminate the later session-evidence hash through the shared map.

## Deviations
