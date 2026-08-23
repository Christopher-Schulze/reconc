# TASK 282: Harden shared filesystem primitives

## Why

Unix `flock` wrappers treat `EINTR` as a permanent acquisition failure instead
of retrying the interrupted syscall. Action-state directory sync can return the
close error while discarding the original sync error. A dead `SplitList` loop
in private path component parsing is misleading. These small primitives sit
under many state, ledger, audit, TASK, and publication paths, so their error
semantics should be exact before further callers build on them.

## Acceptance

- Blocking and non-blocking Unix lock/unlock operations retry only `EINTR` and
  preserve contention (`EAGAIN`/`EWOULDBLOCK`), cancellation polling, timeout,
  descriptor, and Windows behavior.
- Retry loops are bounded by the caller's context/timer where applicable; they
  never transform a contended lock into a busy spin.
- Directory fsync helpers return `errors.Join(syncErr, closeErr)` when both fail
  and never mask the durability failure.
- Private path component splitting uses only path separators, has no
  path-list-separator code, and preserves Windows drive/UNC plus Unix root
  behavior.
- Shared lock/close helpers are reused only where signatures and security
  semantics match; no broad refactor replaces package-specific ownership
  validation.
- Fault-injection tests cover repeated/intermittent `EINTR`, contention,
  cancellation, dual sync/close failure, drive/UNC paths, and malformed input.
- Filelock, actionstate, privatefs, jsonl, audit, TASK, race, platform, and
  complete gates pass.

## Sub-Tasks

- [~] Define retryable syscall errors for each platform lock implementation
- [ ] Retry Unix lock and unlock on `EINTR` without altering contention semantics
- [ ] Join directory sync and close errors consistently
- [ ] Remove the dead path-list loop from private component parsing
- [ ] Audit touched callers for exact close/unlock error propagation
- [ ] Add syscall fault-injection and cross-platform path tests
- [ ] Update low-level filesystem contract documentation if behavior is user-visible
- [ ] Run platform, race, durability, and complete repository verification

## Notes

- Current evidence: `internal/filelock/lock_unix.go` calls `syscall.Flock`
  once; `IsContended` recognizes only contention and `acquireContext` returns
  every other error immediately.
- Current evidence: `internal/actionstate/sync_dir_unix.go` returns `closeErr`
  instead of the earlier `Sync` error when both fail.
- Current evidence: `internal/privatefs/privatefs.go:splitComponents` loops over
  `filepath.SplitList` and discards every value before doing the real
  `filepath.Split` traversal.
- Repeated archive hashing during JSONL rotation and repeated audit-layout
  security stats are not included. They currently validate recovery/security
  boundaries; optimize them only from measured evidence with equivalent proof.

## Deviations

None.
