# TASK 358: Bind global uninstall to owned snapshots

## Why

Global uninstall verifies the receipt and binary, captures backup data through separate observations, and later removes both by pathname. Concurrent replacement can mix backup generations or delete content that was never proven owned.

## Acceptance

- Binary validation, backup capture, deletion, and receipt cleanup remain bound to coherent identities.
- Replacement or symlink substitution at any uninstall boundary fails closed and preserves the replacement.
- Backup metadata and bytes always describe the same binary generation.
- Fault-injection tests cover replacement before backup, publication, and removal.

## Sub-Tasks

- [x] Define coherent owned snapshots for installed binary and receipt.
- [x] Bind backup and conditional removal to those snapshots.
- [x] Add replacement-race and rollback regressions.
- [x] Run global installation and uninstall integration tests.

## Notes

- Verified from OMP session `01a04cc5-6a01-7526-95bb-b2715553daf3`, finding #104.
- Current evidence: `internal/usercli/lifecycle.go` validates and removes through separate pathname operations.
- TASK 251 stabilized the installation lock but did not bind uninstall targets to snapshots.
- `receiptSnapshot` now retains the parsed receipt, opened-file identity, and exact body digest. Binary backups retain the opened identity, mode, size, digest, and private before-image from one strict snapshot.
- Uninstall revalidates the receipt and binary immediately before each removal. Receipt drift after binary removal restores only into an absent binary path; replacements and symlinks are never overwritten.
- Deterministic hooks cover replacement before backup, before binary removal, and before receipt removal. Focused usercli lifecycle/transaction tests, package vet/staticcheck, `make vet`, `make lint`, and `git diff --check` pass.
- Repository-wide `make test-fast`, race, Windows execution, Release Trust, and other heavy gates remain intentionally unrun unless explicitly requested.

## Deviations

- The repository-wide race suite and heavy release gates are deferred per operator instruction; deterministic hook-controlled replacement tests provide bounded uninstall evidence, while CI retains the cross-platform matrix.
