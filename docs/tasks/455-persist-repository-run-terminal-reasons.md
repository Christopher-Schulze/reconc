# TASK 455: Persist repository-run terminal reasons

## Why

The repository-run enum defines `blocked_task` and `no_executable_task`, but the empty-prompt branch returns before persisting either state. Run mode stays enabled without the reason the status/reporting contract advertises.

## Acceptance

- Every non-executable terminal TASK disposition maps to and persists its documented disable reason.
- Blocking TASKs are not treated as completed and can be resumed through the existing lifecycle without losing intent.
- Repeated Stop events are idempotent and do not append duplicate state transitions.
- Tests cover blocked, absent, complete, invalid/no-executable, resumed, and concurrent run-state changes.

## Sub-Tasks

- [ ] Define terminal versus paused repository-run semantics for every disposition.
- [ ] Persist the reason before the empty-prompt early return.
- [ ] Add status/log and resume regressions.
- [ ] Run focused repository-run tests.

## Notes

- Verified from finding 198 in `stop_handler.go` and `repository_run_task.go`.

## Deviations
