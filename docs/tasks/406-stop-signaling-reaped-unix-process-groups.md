# TASK 406: Stop signaling reaped Unix process groups

## Why

After the owned child has exited and `Wait` has reaped it, `unixProcessBoundary.Close` still sends `SIGKILL` to the old negative PID. A reused process-group ID could receive a signal intended for the already-finished downstream.

## Acceptance

- Reaping the owned process permanently disables later Unix group signaling for that boundary.
- Descendant cleanup still terminates the owned group when the leader exits before descendants.
- Close, terminate, kill, attach failure, and repeated-close paths remain idempotent and race-free.
- Deterministic process-boundary tests cover post-reap close and descendant cleanup without relying on PID reuse timing.

## Sub-Tasks

- [ ] Define the boundary state machine around attach, leader exit, descendant ownership, and close.
- [ ] Notify the Unix boundary when the leader is reaped before any close signal.
- [ ] Add deterministic signal-recorder and real subprocess-group regressions.
- [ ] Run focused MCP process tests.

## Notes

- Verified from finding 75.
- `ownedProcess.Close` calls `boundary.Close()` on the already-done path; Unix `Close` currently delegates directly to group `SIGKILL`.

## Deviations
