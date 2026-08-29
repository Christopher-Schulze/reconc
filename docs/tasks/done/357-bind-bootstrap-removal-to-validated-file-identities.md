# TASK 357: Bind bootstrap removal to validated file identities

## Why

Bootstrap removal validates target bytes in one pass and deletes paths in a later pass. A target replaced between those passes can be removed even though the replacement was never validated as owned content.

## Acceptance

- Every removal target is deleted only if it retains the exact identity and bytes validated for the transaction.
- Replacement, symlink substitution, and type changes fail closed without deleting the replacement.
- Multi-target rollback and durability guarantees remain intact.
- Tests cover replacement after preflight and immediately before each removal.

## Sub-Tasks

- [x] Carry bound file identities through bootstrap removal transactions.
- [x] Revalidate identity and ownership at deletion.
- [x] Add multi-target replacement-race regressions.
- [x] Run focused bootstrap transaction tests.

## Notes

- Verified from OMP session `01a04cc5-6a01-7526-95bb-b2715553daf3`, finding #103.
- Current evidence: `internal/bootstrap/remove.go` separates the content-preflight loop from pathname-based removal.
- Removal mutations now retain the validated `os.FileInfo` identity, use strict bounded snapshots before planning and immediately before every mutation, and retain the post-update identity for rollback validation. Replacement, symlink, type, byte, and mode drift fails closed without deleting the replacement; successful removal-parent sync failures are rollback-capable.
- Added deterministic regular-replacement, symlink-substitution, and multi-target rollback regressions. Focused bootstrap removal, managed-candidate acceptance, package vet/staticcheck, and `git diff --check` pass. Repository-wide race, Windows execution, Release Trust, and other heavy gates remain intentionally unrun unless explicitly requested.

## Deviations

The repository-wide race suite is intentionally deferred per operator instruction; deterministic hook-controlled replacement tests provide the bounded race evidence for each removal point.
