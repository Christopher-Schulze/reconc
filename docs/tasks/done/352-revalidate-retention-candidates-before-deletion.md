# TASK 352: Revalidate retention candidates before deletion

## Why

Retention candidates retain path metadata but not a bound filesystem identity. A candidate can be replaced after discovery or liveness checks, causing cleanup to delete a newly created file or directory tree.

## Acceptance

- Every retention deletion proves the target still has the discovered identity and expected type.
- Replacement, symlink substitution, and directory repopulation fail closed without deleting the replacement.
- Recursive deletion never crosses an unvalidated directory identity.
- Adversarial tests cover replacement after discovery and immediately before deletion.

## Sub-Tasks

- [x] Capture deletion-relevant identity in retention candidates.
- [x] Add final bound revalidation and safe directory cleanup.
- [x] Add replacement-race regressions.
- [x] Run focused retention tests and deterministic race tests.

## Notes

- Verified from OMP session `01a04cc5-6a01-7526-95bb-b2715553daf3`, finding #98.
- Current evidence: `internal/retention` removed candidates by path after discovery without an identity comparison at deletion.
- Retention candidates now retain their discovered `os.FileInfo`. Deletion opens and identity-checks the parent with `os.OpenRoot`, revalidates candidate identity and expected regular-file/directory type, and uses the rooted removal API; recursive removal cannot traverse a symlink outside the opened parent.
- Deterministic hooks cover regular replacement, symlink substitution, repopulated-directory replacement, and child-symlink non-traversal. Replacements remain intact and rejected candidates are reported without projected deletion.
- Focused tests passed: `go test ./internal/retention -run 'TestRemoveCandidate|TestRepositoryRuntimeBudgetRemovesOldestInactiveOwnedArtifacts|TestAbandonedTempContractsCoverFilesBuildLocksAndOwnedRoots|TestStateBudgetPreservesActiveAndLatestReceiptsDuringDryRun|TestProjectRootSizingFailsClosedOnIrregularEntries' -count=1`; full package `go test ./internal/retention -count=1` also passed.
- The race detector was not run, per the operator instruction to reserve race-suite runs for explicit requests.

## Deviations

None.
