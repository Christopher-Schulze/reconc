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

- [x] Define aggregate retained-byte accounting for pending calls and all state fields.
- [x] Correct canonical/legacy observation iteration.
- [x] Tighten legacy identity migration and add adversarial fixtures.
- [x] Make epoch-key relativization return independently owned state on every path used by completion capture.
- [x] Run focused agent-session tests.

## Notes

- Verified from findings 40, 41, and 42 plus worker finding 412.
- `observeSessionStateResolved` currently skips both iterations when the two paths are equal; `loadSessionStateResolved` accepts an empty legacy `SessionID` before restamping it.
- `RelativizeEpochKeys` returns its input map for empty input or root-resolution failure; `completionExecutionInputs` then inserts Git-derived epochs and can contaminate the later session-evidence hash through the shared map.
- Changed mutations now preflight the exact normalized serialized bytes. Aggregate overflow preserves the last valid file and persists `session_state` / `byte_budget` taint.
- Pending-call and write-epoch mutators now copy maps before mutation, preserving load/mutate comparison and rollback boundaries.
- Passive observation checks the canonical path once before adding a distinct legacy path; legacy migration requires an exact non-empty stored session identity.
- Epoch relativization returns a detached map for every non-nil input, including empty maps and root-resolution failures.

## Deviations
