# TASK 355: Root TASK lifecycle lock acquisition

## Why

TASK lifecycle mutation locks use path preflight followed by ordinary directory creation and file opening. Intermediate or final path replacement can escape validation or split lock ownership across inodes.

## Acceptance

- TASK lifecycle locks are acquired through rooted, no-follow filesystem operations.
- The held descriptor remains bound to the canonical lock path for every protected mutation.
- Symlink, parent replacement, and lock replacement attempts fail closed.
- Multi-process regressions cover acquisition and held-lock races.

## Sub-Tasks

- [x] Replace path-preflight lock creation with rooted descriptor operations.
- [x] Bind protected mutations to the held lock identity.
- [x] Add symlink and inode-splitting regressions.
- [x] Run focused TASK lifecycle tests and deterministic multi-process race tests.

## Notes

- Verified from OMP session `01a04cc5-6a01-7526-95bb-b2715553daf3`, finding #101.
- Current evidence: `internal/tasklifecycle/transaction.go` separates symlink checks from `MkdirAll` and `OpenFile` acquisition.
- TASK 280 improved lifecycle lock cleanup but did not root acquisition.
- Replaced path-preflight lock setup with `os.Root` acquisition beneath the repository, `.reconc`, and `locks` directories; symlink and non-regular components are rejected and each opened identity is checked against its canonical path.
- `taskMutationLockLease` retains the lock descriptor and parent identities, validates them before protected journal, file, move, recovery, and cleanup mutations, and validates again before release.
- Added POSIX symlink regressions and a deterministic subprocess regression covering lock replacement and unlink inode splitting. Focused lifecycle tests, package vet, staticcheck, and `git diff --check` pass. The repository race detector, Windows tests, Release Trust, and other heavy gates remain intentionally unrun unless explicitly requested.

## Deviations

The task acceptance says “race tests”; the deterministic multi-process replacement/unlink regression is the bounded race proof used here. The repository-wide race suite and Windows execution are intentionally deferred per operator instruction.
