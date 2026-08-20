# TASK 178: Verify bootstrap publications through open file identities

## Why

`publishArtifact` publishes a hard link or exclusive copy, then uses path-based
`Lstat`, `Chmod`, and checksum reads. A concurrent path replacement between
those operations can redirect chmod or cause verification and rollback to act
on an inode other than the one Reconc created. The create-only guarantee does
not by itself protect subsequent path-based mutation.

## Acceptance

- Mode setting, checksum verification, durability, and rollback operate through
  an open descriptor bound to the exact inode created by Reconc.
- The target path is revalidated against that descriptor before success or
  removal; externally replaced targets are preserved and reported.
- Hard-link and exclusive-copy paths share identical identity and cleanup
  guarantees on supported platforms.
- Fault-injection tests cover replacement before chmod, during hashing, before
  cleanup, and Windows mode semantics.
- Bootstrap install, sync, accept, remove, and recovery tests remain green.

## Sub-Tasks

- [x] Define the created-artifact identity record
- [x] Make publication verification descriptor-based
- [x] Make rollback identity-preserving
- [x] Add deterministic swap and failure tests for both publish paths
- [x] Run bootstrap, race, portability, and complete gates

## Notes

- Verified in `internal/bootstrap/transaction.go:406-500` and
  `removeCreatedRecord` in the same file.
- `createdRecord` now owns an open target descriptor plus an opened parent
  `os.Root` and parent identity. Stage writes, hard-link publication, and the
  exclusive-copy fallback retain the exact created descriptor through chmod,
  hashing, sync, and final path validation.
- Rollback hashes through the retained descriptor, validates the opened parent
  and target identities, then removes through the rooted parent. If a target
  or parent is replaced, rollback refuses the removal and leaves the external
  inode untouched. All success paths close records; repository-sync and
  removal-candidate paths now close records they previously discarded.
- Deterministic hooks cover replacement before chmod, during hashing, and
  before cleanup; tests assert external bytes survive and staging residue is
  absent. The rooted exclusive-copy helper is tested separately, and a
  Windows-only descriptor mode test is included.
- `make test` passed after the implementation, including the publication audit,
  harness-pack gates, the full race-enabled Go suite, harness template race
  tests, and release trust. Focused bootstrap swap/copy tests and the Windows
  cross-compile also passed; `git diff --check` is clean.

## Deviations

None.
