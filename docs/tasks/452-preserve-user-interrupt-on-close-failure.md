# TASK 452: Preserve user interrupt on close failure

## Why

The Stop handler intentionally releases an explicit user interrupt, but its deferred run-state file close converts a successful empty-output release into exit 2.

## Acceptance

- An authenticated user interrupt remains released even if run-state close reports an error.
- The close failure remains visible as a bounded diagnostic and never suppresses an existing block/continuation payload.
- Non-interrupt success paths retain their current durability failure semantics unless separately justified.
- Failure-injection tests cover interrupt, block payload, continuation payload, ordinary success, and joined close errors.

## Sub-Tasks

- [ ] Carry the terminal decision class into deferred close handling.
- [ ] Preserve interrupt authority while surfacing cleanup failure.
- [ ] Add deterministic close-failure regressions.
- [ ] Run focused Stop and repository-run tests.

## Notes

- Verified from finding 193 in `internal/runtime/agentsession/stop_handler.go`.

## Deviations
