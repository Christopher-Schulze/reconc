# TASK 298: Propagate caller cancellation into policy scripts

## Why

`RunScript` creates its timeout from `context.Background`, so cancellation of the enclosing check, hook, gateway, or shutdown does not stop a policy script before its own timeout.

## Acceptance

- Script execution accepts caller context and applies the configured timeout as a child deadline.
- Caller cancellation, timeout, process-group termination, kill grace, and returned disposition remain distinguishable.
- Every production caller passes its owned lifecycle context or a documented bounded fallback.
- Cancellation, timeout, process-tree, race, and platform tests pass.

## Sub-Tasks

- [x] Add context to the script execution contract
- [x] Propagate context through evaluator call chains
- [x] Preserve exact timeout and kill-grace semantics
- [x] Add cancellation and shutdown regressions

## Notes

- Evidence: `internal/runtime/script.go:107,126-139` and `internal/runtime/evaluator_rules.go` script callers.
- Owned caller cancellation is distinct from the policy-configured child
  timeout. It propagates as a context error; only the configured timeout becomes
  a fail-closed script timeout disposition.
- Moving the Unix escalation monitor behind `exec.Cmd.Start` removed a real
  race between PID publication and process-group cancellation.
- Verified with focused runtime and gateway regressions, focused race tests,
  complete runtime and CLI packages, Windows amd64 test compilation and vet,
  `make test`, `make vet`, `make lint`, and `make self-host`.

## Deviations

None.
