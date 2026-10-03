# TASK 571: Clean surviving script descendants after cancellation

## Why

Finding F6: cmd.Wait can finish after TERM kills the script parent, close(done),
and stop delayed KILL while a TERM-resistant child with closed output remains.
The existing MCP process boundary already addresses leaderless descendants.

## Acceptance

- A canceled/timed-out script whose parent exits while a child ignores TERM and
  redirects its streams leaves no live descendant or delayed mutation.
- Existing TERM-sensitive and TERM-resistant-parent kill-grace cases remain valid.
- Cleanup is bounded, joined before returning, and never leaves a delayed signal
  goroutine acting on a reaped process identity after the lifecycle has closed.
- Caller cancellation, policy timeout, output bounds, status/exit reporting, and
  successful scripts retain their supported behavior. Windows definitions stay intact.
- Real Unix process regressions, the root suite, vet, lint,
  and development build pass; docs describe the bounded cancellation guarantee.
- The completed TASK is archived, committed, and pushed to origin/main.

## Sub-Tasks

- [ ] Reproduce a leader-exits/child-ignores-TERM script with deterministic readiness.
- [ ] Add bounded group cleanup to the existing script lifecycle using the MCP boundary's proven pattern.
- [ ] Verify monitor ownership, grace timing, no late signals, and unchanged outcome contracts; flush docs.
- [ ] Run validation, review all changes, archive, commit, and push.

## Technical Plan

Inspect configureScriptProcess, monitorScriptProcess, RunScriptContext, and
mcpgateway.unixProcessBoundary.Reaped with its real descendant tests. Keep the
script's process group and TERM/grace behavior. On parent completion, coordinate
the monitor and synchronously clean a surviving owned group when cancellation
has begun; handle absent groups and signal errors explicitly. Ensure monitor
completion is joined before lifecycle return so no timer can signal later.

Use the existing Unix syscall primitives and test helper executable/script
patterns. The regression must redirect child stdin/stdout/stderr so inherited
capture pipes cannot artificially keep Wait alive. Include both caller cancellation
and policy deadline paths. No supervisor service, process-tree polling framework,
extra dependency, or automatic Windows suite is needed.

## Notes

Source owners: internal/runtime/script_process_unix.go, script.go,
script_context_unix_test.go; reference pattern:
internal/mcpgateway/process_unix.go and process_unix_test.go.

## Deviations

None.
