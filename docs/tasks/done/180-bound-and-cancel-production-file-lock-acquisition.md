# TASK 180: Bound and cancel production file-lock acquisition

## Why

`filelock.Lock` and `RLock` use blocking OS locks. Multiple production paths
call them directly, so a contended or orphaned operational workflow can wait
without respecting command cancellation or a Reconc-owned timeout. Other
subsystems already implement bounded `TryLock` loops, creating inconsistent
availability behavior.

## Acceptance

- Every production lock acquisition is classified as immediate, bounded, or
  intentionally blocking with an explicit documented owner and lifecycle.
- User-facing and hook-facing paths accept cancellation and a finite timeout;
  contention produces a typed, actionable error rather than an unbounded wait.
- Lock ordering is documented and tested for nested session, stop-policy,
  receipt, audit, retention, task, and command-proof operations.
- Tests cover cancellation, timeout, unlock errors, process-exit release, and
  high-contention success under the race detector on Unix and Windows.

## Sub-Tasks

- [x] Inventory and classify every production `Lock` and `RLock` call
- [x] Add a canonical context-aware lock API
- [x] Migrate bounded callsites and preserve immediate-lock semantics
- [x] Add contention, cancellation, and ordering tests
- [x] Run race, platform, and complete Go gates

## Notes

- The raw blocking implementation is in `internal/filelock/lock_unix.go`;
  `internal/actionstate/secure_fs.go` contains a reusable behavioral model.
- Production inventory found no remaining direct blocking `filelock.Lock` or
  `RLock` call. Compiler/bootstrap/JSONL/action-state paths retain immediate
  `TryLock` semantics; audit, hooks, TASK, receipt, retention, command-proof,
  and hook-runtime state paths now use `LockContext` with finite budgets.
- `internal/filelock` now exposes typed timeout/cancellation errors and a
  context-aware exclusive/shared acquisition loop. Tests cover pre-canceled
  contexts, finite contention timeout, release-then-success, closed-descriptor
  unlock errors, and race-safe operation. Lock ordering is documented in
  `docs/architecture.md`.
- The full `make test` gate passed after the high-contention hook-runtime
  budget was set to 30 seconds: publication audit, harness-pack validation,
  race-enabled Go packages, harness template race tests, and release trust all
  completed successfully.

## Deviations

None.
