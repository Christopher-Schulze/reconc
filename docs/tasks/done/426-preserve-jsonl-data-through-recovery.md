# TASK 426: Preserve JSONL data through recovery

## Why

Tail trimming can publish an empty file when the retained suffix has no newline, an orphan `.append-backup.N` blocks every later append without a journal recovery path, and noncanonical archive suffixes are discovered but then addressed under a different canonical name.

## Acceptance

- Tail trimming never destroys the only retained record or publishes empty data solely because no newline exists in the selected suffix.
- Orphan backups are handled by an identity-validated, non-destructive recovery or explicit remediation path.
- Archive discovery accepts only canonical suffixes or carries the exact discovered path consistently.
- Adversarial tests cover single long records, crash residues, suffix aliases, concurrent replacement, and recovery replay.

## Sub-Tasks

- [x] Define canonical live/archive/backup naming and truncation invariants.
- [x] Make tail selection record-aware at the boundary.
- [x] Add safe orphan handling and exact-path archive processing.
- [x] Run focused JSONL recovery and rotation tests.

## Notes

- Verified from findings 106, 150, and 152.
- Tail repair now returns `ErrNoCompleteRecordWithinLimit` without publication
  when the bounded suffix contains no complete record.
- Noncanonical numeric suffixes are excluded during discovery instead of being
  parsed and later addressed through a different canonical path.
- Journal-free backup residues are copied to a layout- and content-bound hidden
  recovery path. Source and recovery identities, contents, modes, link counts,
  and the writer-lock lease are revalidated before the transaction name is
  removed; the first attempt returns `OrphanAppendBackupError` with both paths.
- The complete `internal/jsonl` package and focused action-ledger and audit
  append/recovery/rotation tests passed. Queue-wide heavy gates remain deferred
  to the final pass as requested.

## Deviations
