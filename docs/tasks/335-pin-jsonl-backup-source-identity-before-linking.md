# TASK 335: Pin JSONL backup source identity before linking

## Why

Rotation backup creation validates a source path, then calls `os.Link(source, backupPath)` by path. Replacement between validation and link can bind the recovery backup to different content.

## Acceptance

- Backup publication is tied to the exact opened source identity and validated content generation.
- A source swap, symlink, hardlink-count change, or backup collision fails closed without overwriting either object.
- Copy fallback, checksum validation, cleanup, journal recovery, and archive ordering remain exact.
- Adversarial replacement and crash tests pass on supported platforms.

## Sub-Tasks

- [ ] Map backup source validation and link semantics per platform
- [ ] Introduce a descriptor- or identity-bound publication protocol
- [ ] Add source-swap and collision regressions
- [ ] Run JSONL, ledger, audit, and recovery gates

## Notes

- Evidence: `internal/jsonl/journal.go` `createAppendBackupWithLayout`.

## Deviations

None.
