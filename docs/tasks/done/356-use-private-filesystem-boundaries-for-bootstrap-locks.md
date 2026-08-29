# TASK 356: Use private filesystem boundaries for bootstrap locks

## Why

Bootstrap repository locks use raw path-based directory and file operations. The final lock path can follow a symlink, and ownership, link count, ACL, descriptor identity, and replacement invariants are not enforced consistently with other private state.

## Acceptance

- Bootstrap locks use the repository's private-filesystem security boundary.
- Lock acquisition rejects symlinks, non-regular files, unexpected ownership, insecure modes or ACLs, and replacement races.
- The held lock remains bound to the validated canonical path.
- Cross-platform tests cover hostile lock and parent layouts.

## Sub-Tasks

- [x] Route bootstrap lock directories and files through private filesystem primitives.
- [x] Preserve bootstrap timeout and recovery semantics.
- [x] Add Unix and Windows security regressions.
- [x] Run bootstrap, private filesystem, and deterministic replacement tests.

## Notes

- Verified from OMP session `01a04cc5-6a01-7526-95bb-b2715553daf3`, finding #102.
- Current evidence: `internal/bootstrap/repository_lock.go` uses `MkdirAll`, `Lstat`, `Chmod`, and `OpenFile` directly.
- Replaced raw repository-lock directory/file setup with `privatefs.RepairDirectory` and `privatefs.OpenLock`; ownership, private mode/ACL, regular-file type, one-link state, rooted parent identity, and replacement checks now share the established private boundary while `TryLock` timeout and cleanup semantics remain unchanged.
- Added bootstrap integration coverage for symlinked repository lock directories and lock files; existing Unix/Windows privatefs security and replacement suites cover cross-platform hostile layouts. Focused bootstrap/privatefs tests, package vet/staticcheck, and `git diff --check` pass. Repository-wide race, Windows execution, Release Trust, and other heavy gates remain intentionally unrun unless explicitly requested.

## Deviations

The repository-wide race suite is intentionally deferred per operator instruction; deterministic privatefs replacement tests and focused bootstrap concurrency tests are the bounded race evidence for this task.
