# TASK 389: Make run-log following duplicate-safe and incremental

## Why

Run-log cursors hash only record content, so identical adjacent decisions share a cursor and backward lookup can skip duplicates. Follow mode also reloads, decodes, marshals, and hashes the complete bounded ring every 500 ms, while text rendering truncates Unicode session IDs by bytes.

## Acceptance

- Every retained record has a stable occurrence identity even when record bodies are identical.
- Rotation and lost-cursor detection remain fail closed without skipping or duplicating appended records.
- Follow mode reads only new or changed ring data after its baseline and does not rescan unchanged archives each tick.
- Unicode session IDs are shortened on rune boundaries; benchmarks cover idle polling, append, duplicate records, and rotation.

## Sub-Tasks

- [ ] Define a cursor identity that binds record occurrence and ring position safely.
- [ ] Implement incremental follow with rotation-aware resynchronization.
- [ ] Add duplicate, append-race, rotation, malformed-ring, and Unicode tests.
- [ ] Run focused CLI tests and follow benchmarks.

## Notes

- Verified from findings 34, 35, and 37.
- The original marshal-error subclaim is not actionable for the current concrete `RunDecision` shape; duplicate identity and polling cost are confirmed.

## Deviations
