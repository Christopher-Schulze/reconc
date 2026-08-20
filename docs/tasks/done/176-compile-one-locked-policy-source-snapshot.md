# TASK 176: Compile one locked policy-source snapshot

## Why

`CompileRepoPolicy` loads every policy source before acquiring the repository
compile lock. Two compilers can therefore capture different pre-lock bundles
and publish them in lock-acquisition order rather than source-freshness order.
The lock serializes writes but does not bind publication to a source snapshot
captured while that lock is held.

## Acceptance

- The publication lock is acquired before the authoritative source snapshot is
  captured, or the complete snapshot is revalidated after lock acquisition.
- Concurrent compiles cannot let an older source snapshot overwrite a newer
  compiled result.
- Compile-lock creation rejects symlink, irregular-file, identity-swap, and
  unsafe parent-directory cases without touching an unrelated inode.
- Concurrency tests deterministically reproduce the old ordering race and prove
  the published lockfile matches the final locked source snapshot.
- Render-only compiler APIs remain write-free and retain their existing
  transaction-owner contract.

## Sub-Tasks

- [x] Define compile snapshot and lock ordering
- [x] Harden compile-lock filesystem handling
- [x] Move or revalidate source capture under the lock
- [x] Add deterministic concurrent-compile and hostile-path tests
- [x] Run compiler, race, and complete Go gates

## Notes

- Verified in `internal/compiler/compiler.go:115-145` and
  `internal/compiler/lock.go`.
- Refresh now performs only root discovery before locking. It loads and renders
  the complete source bundle under the acquired lock, using the original start
  path, and refuses publication if authoritative discovery resolves to a
  different repository root.
- Compile-lock creation uses opened `os.Root` identities for the repository and
  `.reconc` directory. It rejects symlinked or irregular parents and lock
  objects, validates the opened lock against its pre-open and current identity,
  and revalidates the parent before attempting the OS lock. Render-only APIs do
  not enter this path and remain write-free.
- Deterministic tests hold the source loader inside the compile lock, mutate the
  source, reject a concurrent compiler, and prove the published digest matches
  the final locked snapshot. Additional tests cover root drift, parent and lock
  irregularity, parent and lock symlinks, identity replacement, unchanged
  target bytes/mode, and persistent lock reuse.
- Verification passed: `go test ./internal/compiler`,
  `go test -race ./internal/compiler`, `git diff --check`, and the complete
  `make test` gate including release trust.

## Deviations

None.
