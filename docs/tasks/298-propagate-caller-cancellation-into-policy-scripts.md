# TASK 298: Propagate caller cancellation into policy scripts

## Why

`RunScript` creates its timeout from `context.Background`, so cancellation of the enclosing check, hook, gateway, or shutdown does not stop a policy script before its own timeout.

## Acceptance

- Script execution accepts caller context and applies the configured timeout as a child deadline.
- Caller cancellation, timeout, process-group termination, kill grace, and returned disposition remain distinguishable.
- Every production caller passes its owned lifecycle context or a documented bounded fallback.
- Cancellation, timeout, process-tree, race, and platform tests pass.

## Sub-Tasks

- [ ] Add context to the script execution contract
- [ ] Propagate context through evaluator call chains
- [ ] Preserve exact timeout and kill-grace semantics
- [ ] Add cancellation and shutdown regressions

## Notes

- Evidence: `internal/runtime/script.go:107,126-139` and `internal/runtime/evaluator_rules.go` script callers.

## Deviations

None.
