# TASK 351: Bind JSONL layout security to opened files

## Why

JSONL backup reads validate layout security through one path lookup and read bytes through a later snapshot. A replacement between those operations can make validated security metadata and consumed bytes describe different files.

## Acceptance

- JSONL file security and byte reads are validated through one bound descriptor identity.
- Path replacement between layout validation and reading fails closed.
- Custom layout-security implementations retain a descriptor-bound validation contract.
- Race tests cover replacement and symlink substitution at every boundary.

## Sub-Tasks

- [x] Define descriptor-bound JSONL layout security validation.
- [x] Integrate security checks with bounded regular-file snapshots.
- [x] Update custom security implementations and callers.
- [x] Run focused JSONL replacement and symlink-substitution tests.

## Notes

- Verified from OMP session `01a04cc5-6a01-7526-95bb-b2715553daf3`, finding #96.
- Current evidence: `internal/jsonl/journal.go` called path-based layout validation before a separate regular-file snapshot.
- `LayoutSecurity` now requires `ValidateOpenedJSONLFile`; bounded JSONL reads validate security inside the same opened-file snapshot, while append-backup sources validate the opened descriptor before reading bytes.
- Audit and private action-state security implementations provide descriptor-bound validation. Deterministic tests cover regular replacement and symlink substitution for bounded backup reads, tail reads, and append-backup source opening; rejected reads return no bytes.
- Focused tests passed: `go test ./internal/jsonl -run 'TestLayoutSecurity(ReadRejectsReplacementDuringBoundSnapshot|TailRejectsReplacementDuringBoundSnapshot)|TestOpenAppendBackupSourceRejectsSecurityReplacement|TestLayoutSecurity(PublishesFirstLockOnlyAfterSecurity|CoversRotationArchivesAndRecoveryBackups|SecuresEveryNewDurableFileAndRejectsExistingDrift|IdentityIsBoundIntoRecoveryJournal|AcceptsPreSecurityRecoveryJournal)' -count=1`; `go test ./internal/actionstate -run '^TestPrivateProjectStorageSecuresAndValidatesOnlyBoundJSONLPaths$' -count=1`.
- The race detector was not run, per the operator instruction to reserve race-suite runs for explicit requests.

## Deviations

None.
