# TASK 406: Stop signaling reaped Unix process groups

## Why

After the owned child has exited and `Wait` has reaped it, `unixProcessBoundary.Close` still sends `SIGKILL` to the old negative PID. A reused process-group ID could receive a signal intended for the already-finished downstream.

## Acceptance

- Reaping the owned process permanently disables later Unix group signaling for that boundary.
- Descendant cleanup still terminates the owned group when the leader exits before descendants.
- Close, terminate, kill, attach failure, and repeated-close paths remain idempotent and race-free.
- Deterministic process-boundary tests cover post-reap close and descendant cleanup without relying on PID reuse timing.

## Sub-Tasks

- [x] Define the boundary state machine around attach, leader exit, descendant ownership, and close.
- [x] Notify the Unix boundary when the leader is reaped before any close signal.
- [x] Add deterministic signal-recorder and real subprocess-group regressions.
- [x] Run focused MCP process tests.

## Notes

- Verified from finding 75.
- `ownedProcess.Close` calls `boundary.Close()` on the already-done path; Unix `Close` currently delegates directly to group `SIGKILL`.
- Confirmed in current code: the wait goroutine reaped the leader and closed `done` without updating the Unix boundary, so every later `Close` signaled the stale negative PID.
- Unix boundaries now serialize explicit unattached, attached, reaped, and closed states. `Terminate`, `Kill`, `Reaped`, and `Close` are idempotent under one mutex.
- The wait owner calls `Reaped` immediately after `exec.Cmd.Wait`. A still-live group receives one final descendant cleanup signal; an absent group receives none. Either outcome clears the PID before `done` becomes visible, so later close/terminate/kill calls cannot signal it.
- Deterministic signal recorders cover absent groups, descendant groups, repeated signals, invalid attach transitions, and post-reap close. A real leader-exits-first subprocess proves the remaining descendant is terminated.
- Verification passed: complete `internal/mcpgateway` tests, `make test-fast`, `gofmt`, and `git diff --check`. Windows behavior was maintained but not executed locally per queue policy.

## Deviations
