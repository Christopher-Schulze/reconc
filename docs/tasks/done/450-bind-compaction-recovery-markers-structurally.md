# TASK 450: Bind compaction recovery markers structurally

## Why

Post-compaction recovery suppresses its packet whenever the arbitrary host/model summary contains the substring `reconc-context-v1`. Incidental or adversarial text can therefore disable recovery without proving that Reconc's packet was preserved.

## Acceptance

- Suppression requires an exact, structured marker at the documented recovery boundary, not substring presence.
- Marker-like text in prose, code, paths, or quoted payloads does not suppress recovery.
- Repeated genuine recovery remains idempotent and bounded.
- Tests cover prefix/suffix collisions, multiline summaries, duplicate packets, Unicode, truncation, and malformed markers.

## Sub-Tasks

- [x] Define a parseable recovery-envelope marker and exact placement.
- [x] Replace substring detection with bounded structural validation.
- [x] Add false-positive and idempotency regressions.
- [x] Run focused compaction tests.

## Notes

- Verified from finding 190 in `internal/runtime/agentsession/compaction.go`.
- Recovery packets now use a final standalone `reconc-context-v1` envelope whose exact begin line carries a lowercase SHA-256 digest of the complete bounded body and whose exact end line terminates the summary block.
- Suppression scans at most the final 64 KiB and requires an intact digest-valid final envelope. Ordinary marker prose, code, paths, quoted payloads, prefix/suffix collisions, trailing unrelated text, malformed digests, and truncation cannot suppress recovery.
- Genuine packets, including Unicode and repeated packets, remain idempotent. The complete envelope remains within the existing 4 KiB context limit after body truncation.
- Focused compaction, native-event adaptation, collision, malformed-envelope, Unicode, duplicate, truncation, and bound tests passed.

## Deviations

- Per Christopher's queue-wide gate instruction, broad full/race/vet/lint/release gates are deferred until TASK 460.
