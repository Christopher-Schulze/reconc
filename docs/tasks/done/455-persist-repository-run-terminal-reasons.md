# TASK 455: Persist repository-run terminal reasons

## Why

The repository-run enum defines `blocked_task` and `no_executable_task`, but the empty-prompt branch returns before persisting either state. Run mode stays enabled without the reason the status/reporting contract advertises.

## Acceptance

- Every non-executable terminal TASK disposition maps to and persists its documented disable reason.
- Blocking TASKs are not treated as completed and can be resumed through the existing lifecycle without losing intent.
- Repeated Stop events are idempotent and do not append duplicate state transitions.
- Tests cover blocked, absent, complete, invalid/no-executable, resumed, and concurrent run-state changes.

## Sub-Tasks

- [x] Define terminal versus paused repository-run semantics for every disposition.
- [x] Persist the reason before the empty-prompt early return.
- [x] Add status/log and resume regressions.
- [x] Run focused repository-run tests.

## Notes

- Verified from finding 198 in `stop_handler.go` and `repository_run_task.go`.
- Non-executable dispositions now persist their existing exact mappings: blocked -> `blocked_task`, complete -> `task_complete`, absent -> `task_plane_absent`, and invalid/unknown -> `no_executable_task`.
- A blocked TASK disables autonomous continuation without being classified as complete. `reconc run on` clears the reason and resumes the unchanged TASK intent through the normal continuation path.
- Terminal transitions bind the observed run epoch. A concurrent disable or off/on cycle wins instead of being overwritten by a stale Stop observation.
- Status and decision-log regressions cover every reason, repeated Stop idempotency, resume, and deterministic concurrent-epoch rejection. The focused repository-run suite passed in 4.31 seconds.

## Deviations

- Per user direction, full module, race, vet, lint, release, and platform gates are deferred until TASK 460 so they run once over the final queue state.
