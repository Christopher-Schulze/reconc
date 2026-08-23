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

- [x] Define retryable syscall errors for each platform lock implementation
- [x] Retry Unix lock and unlock on `EINTR` without altering contention semantics
- [x] Join directory sync and close errors consistently
- [x] Remove the dead path-list loop from private component parsing
- [x] Audit touched callers for exact close/unlock error propagation
- [x] Add syscall fault-injection and cross-platform path tests
- [x] Update low-level filesystem contract documentation if behavior is user-visible
- [x] Run platform, race, durability, and complete repository verification

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
- Unix blocking, non-blocking, shared, exclusive, and unlock operations now
  share one `EINTR` retry primitive. Context-bound acquisition passes its
  deadline into that primitive and reclassifies parent cancellation versus
  timeout exactly; contention still uses the existing 5 ms poll cadence.
- Action-state directory publication joins fsync and close errors through a
  narrow interface-tested helper. The two adjacent helpers with the same
  proven masking pattern, bootstrap removal and managed hook artifact parent
  sync, received the same surgical join. Existing policy-proof and atomic-file
  helpers already joined both failures.
- Private component parsing removed the unrelated `SplitList` loop and now
  appends then reverses once, avoiding both dead path-list semantics and
  quadratic slice prepending. Unix separator/colon cases plus Windows
  drive/UNC roots compile under their native build constraints.
- Verification passed: syscall fault injection for intermittent/repeated
  `EINTR`, contention and cancellation/deadline; dual fsync/close failures;
  focused package and Race tests; Windows/Linux cross-compilation; complete
  fast repository tests; publication/harness audits; Vet; Staticcheck; and
  self-hosting.

## Deviations

None.
