# TASK 447: Bound Git process cancellation

## Why

Git commands use `exec.CommandContext` without `WaitDelay`. A killed Git parent can leave grandchildren holding inherited stdout/stderr pipes, causing `Run` or bounded output collection to outlive the context deadline.

## Acceptance

- Every Git subprocess has a bounded post-cancellation wait and explicit pipe/process cleanup.
- Direct child and descendant processes cannot hold Reconc past the documented deadline indefinitely.
- Normal exit status, stderr capture, and Git error classification remain unchanged.
- Deterministic helper-process tests cover inherited pipes, cancellation, process exit races, and repeated cleanup on Unix and Windows-supported paths.

## Sub-Tasks

- [x] Centralize Git command cancellation policy in `internal/gitexec`.
- [x] Apply bounded `WaitDelay` and platform-appropriate kill/pipe handling.
- [x] Add deterministic descendant-pipe regressions.
- [x] Run focused gitexec and command-proof tests.

## Notes

- Verified from finding 184; runtime script processes already provide a local bounded-wait reference.
- All non-test Git process construction now lives in `internal/gitexec`; configuration-aware hook discovery retains repository Git config through its explicitly named constructor, while other callers keep the hermetic command contract.
- Git commands use a 250 ms `WaitDelay`. Cancellation kills the Unix process group or Windows child immediately; the standard wait bound then closes inherited pipes. Cross-platform helper tests escape a grandchild deliberately, repeat cleanup, race cancellation against exit, and preserve exit code 7 plus exact stderr.

## Deviations
