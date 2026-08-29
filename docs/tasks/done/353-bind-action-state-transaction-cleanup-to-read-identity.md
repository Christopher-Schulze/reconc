# TASK 353: Bind action-state transaction cleanup to read identity

## Why

Action-state recovery reads a transaction file and later removes the same pathname without carrying the read identity through cleanup. A replacement can therefore be deleted as if it were the recovered transaction.

## Acceptance

- Transaction cleanup is conditional on the target retaining the exact identity that was read and validated.
- Replacement or symlink substitution before removal fails closed and preserves the replacement.
- Parent durability behavior remains correct after successful cleanup.
- Tests cover replacement during recovery and the final removal window.

## Sub-Tasks

- [x] Carry a bound transaction identity from read through recovery.
- [x] Implement conditional removal against that identity.
- [x] Add replacement-race regression tests.
- [x] Run focused action-state recovery tests.

## Notes

- Verified from OMP session `01a04cc5-6a01-7526-95bb-b2715553daf3`, finding #99.
- Current evidence: `internal/actionstate/store.go` reads `state-transaction.json` and later removes it through a fresh pathname lookup.
- Recovery now retains the validated private regular-file snapshot identity. Cleanup reopens the private parent through `os.OpenRoot`, revalidates parent and journal identity twice around the final removal hook, removes only the rooted matching file, and synchronizes that bound root.
- Deterministic tests cover regular replacement after recovery read, regular replacement in the final removal window, and symlink substitution. Replacements remain present and cleanup fails closed.
- Focused tests passed: `go test ./internal/actionstate -run 'TestRecoverTransaction|TestBudgetStoreRecoversPublishedTransactionAfterInterruption|TestBudgetStorePreflightsOversizedStateBeforePublishingJournal|TestReadExistingEvidenceDoesNotRepairUnresolvedTransaction' -count=1 -timeout=30s`.
- Full action-state package test passed: `go test ./internal/actionstate -count=1 -timeout=60s`; focused package vet passed: `go vet ./internal/actionstate`.
- Race detector and heavy repository gates were not run, per the operator instruction to reserve those runs for explicit requests.

## Deviations

None.
