# TASK 354: Prevent JSONL lock inode splitting

## Why

A JSONL writer verifies a lock descriptor against its path only at acquisition time. If the lock path is later unlinked or replaced, another writer can lock a new inode while the first writer still holds the old one.

## Acceptance

- One logical JSONL layout cannot have concurrent owners through different lock inodes.
- Lock-path replacement or unlinking during a held lease is detected before protected mutations.
- Lock acquisition and release preserve existing timeout and cleanup behavior.
- Multi-process tests reproduce and prevent inode-splitting races.

## Sub-Tasks

- [x] Define stable JSONL lock-path ownership across the protected operation.
- [x] Add rooted identity checks at mutation boundaries or an equivalent unreplaceable lock design.
- [x] Add adversarial multi-process regressions.
- [x] Run JSONL integration tests; reserve the race detector for an explicit request.

## Notes

- Verified from OMP session `01a04cc5-6a01-7526-95bb-b2715553daf3`, finding #100.
- Current evidence: `internal/jsonl/lock.go` validates descriptor-to-path identity once before invoking the protected callback.
- TASK 251 fixed the installation lock, not the JSONL layout lock.
- `withLayoutLockLeaseContext` carries the opened lock identity through every production JSONL writer, recovery, enforcement, rotation, journal, backup, trim, and cleanup mutation. Rooted parent operations revalidate the lease before and after directory synchronization; the outer lock scope validates it again before unlock.
- A replacement or unlink after lock acquisition now fails closed before the stale owner writes, rotates, restores, or removes. The replacement owner can acquire the new inode, but the stale owner cannot publish through it.
- `TestLockLeaseRejectsCrossProcessInodeSplitting` uses a real subprocess and covers both lock-path replacement and unlink while the original owner is paused inside its prepare callback. The replacement owner appends successfully; the stale owner returns the lease-change error and contributes no record bytes.
- Focused validation passed: `go test ./internal/jsonl -count=1 -timeout=90s`, `go vet ./internal/jsonl`, `make vet`, and `make lint`.
- `make test-fast TEST_PARALLELISM=8` was intentionally interrupted after the unrelated full audit/bootstrap suites exceeded 90 seconds; no race detector, Windows tests, or release-trust gate was run, per operator instruction.

## Deviations

None.
