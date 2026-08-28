# TASK 319: Implement the completion-state retry contract

## Why

Completion evaluation detects repository, policy, or session drift and tells the caller to retry, but neither `Evaluate` nor its CLI callers perform a bounded retry. Concurrent normal activity becomes a manual failure.

## Acceptance

- Completion uses a documented bounded retry count for retryable state drift only.
- Non-retryable policy, proof, Git, task, encoding, and I/O failures return immediately.
- Each attempt owns a fresh coherent capture and never reuses stale report or proof state.
- Persistent mutation exhausts the bound with an exact diagnostic; stable second attempts succeed.

## Sub-Tasks

- [x] Introduce a typed retryable drift error
- [x] Choose one owner for bounded retries
- [x] Add transient and persistent mutation tests
- [x] Run completion, CLI, task, and race gates

## Notes

- Evidence: `internal/completiongate/gate.go:20-35,108-142,260-264` and `internal/cli/workflow_cmd.go:699-704,768-773`.
- `completiongate.Evaluate` owns one bounded retry (two coherent attempts total) and retries only `RetryableStateDriftError`; CLI callers retain one shared policy and error boundary.
- Every attempt reconstructs the before-state, report, policy inputs, task checks, proof binding, and after-state; non-retryable errors return immediately and stale reports are discarded.
- Transient drift succeeds on the fresh second attempt; persistent drift returns exactly `repository, policy, or active-session state changed during completion evaluation after 2 attempts; retry limit exhausted`.
- Verification: completion-gate transient/persistent/non-retryable retry regressions, completion and CLI suites, task contracts, `make test-fast`, `make vet`, Staticcheck v0.8.1, and `make self-host` all passed.

## Deviations

None.
