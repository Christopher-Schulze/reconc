# TASK 389: Make run-log following duplicate-safe and incremental

## Why

Run-log cursors hash only record content, so identical adjacent decisions share a cursor and backward lookup can skip duplicates. Follow mode also reloads, decodes, marshals, and hashes the complete bounded ring every 500 ms, while text rendering truncates Unicode session IDs by bytes.

## Acceptance

- Every retained record has a stable occurrence identity even when record bodies are identical.
- Rotation and lost-cursor detection remain fail closed without skipping or duplicating appended records.
- Follow mode reads only new or changed ring data after its baseline and does not rescan unchanged archives each tick.
- Unicode session IDs are shortened on rune boundaries; benchmarks cover idle polling, append, duplicate records, and rotation.

## Sub-Tasks

- [x] Define a cursor identity that binds record occurrence and ring position safely.
- [x] Implement incremental follow with rotation-aware resynchronization.
- [x] Add duplicate, append-race, rotation, malformed-ring, and Unicode tests.
- [x] Run focused CLI tests and follow benchmarks.

## Notes

- Verified from findings 34, 35, and 37.
- The original marshal-error subclaim is not actionable for the current concrete `RunDecision` shape; duplicate identity and polling cost are confirmed.
- The follower now binds the cursor to file identity, byte range, and raw-line SHA-256. It reuses unchanged decoded members, validates their retained tail occurrence, and decodes only appended suffixes or replaced members.
- Focused agent-session and CLI packages pass. With 2,048 retained records, the 10-iteration idle benchmark changed from 2,754,254 ns/op, 5,062,367 B/op, and 20,666 allocs/op for full snapshots to 82,783 ns/op, 7,838 B/op, and 65 allocs/op for the follower.
- `make test-fast` passed for the root and portable-template modules; formatting and generated reference documentation are current.

## Deviations
