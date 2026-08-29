# TASK 447: Bound Git process cancellation

## Why

Git commands use `exec.CommandContext` without `WaitDelay`. A killed Git parent can leave grandchildren holding inherited stdout/stderr pipes, causing `Run` or bounded output collection to outlive the context deadline.

## Acceptance

- Every Git subprocess has a bounded post-cancellation wait and explicit pipe/process cleanup.
- Direct child and descendant processes cannot hold Reconc past the documented deadline indefinitely.
- Normal exit status, stderr capture, and Git error classification remain unchanged.
- Deterministic helper-process tests cover inherited pipes, cancellation, process exit races, and repeated cleanup on Unix and Windows-supported paths.

## Sub-Tasks

- [ ] Centralize Git command cancellation policy in `internal/gitexec`.
- [ ] Apply bounded `WaitDelay` and platform-appropriate kill/pipe handling.
- [ ] Add deterministic descendant-pipe regressions.
- [ ] Run focused gitexec and command-proof tests.

## Notes

- Verified from finding 184; runtime script processes already provide a local bounded-wait reference.

## Deviations
