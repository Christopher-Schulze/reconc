# TASK 335: Pin JSONL backup source identity before linking

## Why

Rotation backup creation validates a source path, then calls `os.Link(source, backupPath)` by path. Replacement between validation and link can bind the recovery backup to different content.

## Acceptance

- Backup publication is tied to the exact opened source identity and validated content generation.
- A source swap, symlink, hardlink-count change, or backup collision fails closed without overwriting either object.
- Copy fallback, checksum validation, cleanup, journal recovery, and archive ordering remain exact.
- Adversarial replacement and crash tests pass on supported platforms.

## Sub-Tasks

- [x] Map backup source validation and link semantics per platform
- [x] Introduce a descriptor- or identity-bound publication protocol
- [x] Add source-swap and collision regressions
- [x] Run JSONL, ledger, audit, and recovery gates

## Notes

- Evidence: `internal/jsonl/journal.go` `createAppendBackupWithLayout`.
- Backup publication reads from an opened regular-file snapshot, verifies source identity, metadata, bytes, and platform link count before and after publication, and checks that hard-link publication targets the opened source inode.
- Existing backup targets are collision errors; copy fallback uses create-only publication so neither a raced target nor a stale artifact is replaced.
- Regressions cover source replacement, hard-link-count changes, existing-target collisions, and successful source linking. Validation: `go test ./internal/jsonl -count=1`, `go test -race ./internal/jsonl -count=1`, `go vet ./internal/jsonl`, and `GOOS=windows GOARCH=amd64 go test -c -o /dev/null ./internal/jsonl`.

## Deviations

None.
