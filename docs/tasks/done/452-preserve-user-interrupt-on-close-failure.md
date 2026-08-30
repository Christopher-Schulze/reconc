# TASK 452: Preserve user interrupt on close failure

## Why

The Stop handler intentionally releases an explicit user interrupt, but its deferred run-state file close converts a successful empty-output release into exit 2.

## Acceptance

- An authenticated user interrupt remains released even if run-state close reports an error.
- The close failure remains visible as a bounded diagnostic and never suppresses an existing block/continuation payload.
- Non-interrupt success paths retain their current durability failure semantics unless separately justified.
- Failure-injection tests cover interrupt, block payload, continuation payload, ordinary success, and joined close errors.

## Sub-Tasks

- [x] Carry the terminal decision class into deferred close handling.
- [x] Preserve interrupt authority while surfacing cleanup failure.
- [x] Add deterministic close-failure regressions.
- [x] Run focused Stop and repository-run tests.

## Notes

- Verified from finding 193 in `internal/runtime/agentsession/stop_handler.go`.
- Deferred run-state close handling now carries an explicit terminal decision class. Authenticated interrupts keep exit 0, while ordinary empty success still converts a durability close failure to exit 2.
- Close failures append one bounded diagnostic without replacing policy-block, continuation, prior failure, or interrupt diagnostics.
- Deterministic injected-close tests cover interrupt, policy block, continuation, ordinary success, joined diagnostics, and the integrated Stop wiring. The focused `TestRepoRun` suite passed in 3.43 seconds.

## Deviations

- Per user direction, full module, race, vet, lint, release, and platform gates are deferred until TASK 460 so they run once over the final queue state.
