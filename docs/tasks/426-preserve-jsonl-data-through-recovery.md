# TASK 426: Preserve JSONL data through recovery

## Why

Tail trimming can publish an empty file when the retained suffix has no newline, an orphan `.append-backup.N` blocks every later append without a journal recovery path, and noncanonical archive suffixes are discovered but then addressed under a different canonical name.

## Acceptance

- Tail trimming never destroys the only retained record or publishes empty data solely because no newline exists in the selected suffix.
- Orphan backups are handled by an identity-validated, non-destructive recovery or explicit remediation path.
- Archive discovery accepts only canonical suffixes or carries the exact discovered path consistently.
- Adversarial tests cover single long records, crash residues, suffix aliases, concurrent replacement, and recovery replay.

## Sub-Tasks

- [ ] Define canonical live/archive/backup naming and truncation invariants.
- [ ] Make tail selection record-aware at the boundary.
- [ ] Add safe orphan handling and exact-path archive processing.
- [ ] Run focused JSONL recovery and rotation tests.

## Notes

- Verified from findings 106, 150, and 152.

## Deviations
