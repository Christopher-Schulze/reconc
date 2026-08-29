# TASK 450: Bind compaction recovery markers structurally

## Why

Post-compaction recovery suppresses its packet whenever the arbitrary host/model summary contains the substring `reconc-context-v1`. Incidental or adversarial text can therefore disable recovery without proving that Reconc's packet was preserved.

## Acceptance

- Suppression requires an exact, structured marker at the documented recovery boundary, not substring presence.
- Marker-like text in prose, code, paths, or quoted payloads does not suppress recovery.
- Repeated genuine recovery remains idempotent and bounded.
- Tests cover prefix/suffix collisions, multiline summaries, duplicate packets, Unicode, truncation, and malformed markers.

## Sub-Tasks

- [ ] Define a parseable recovery-envelope marker and exact placement.
- [ ] Replace substring detection with bounded structural validation.
- [ ] Add false-positive and idempotency regressions.
- [ ] Run focused compaction tests.

## Notes

- Verified from finding 190 in `internal/runtime/agentsession/compaction.go`.

## Deviations
