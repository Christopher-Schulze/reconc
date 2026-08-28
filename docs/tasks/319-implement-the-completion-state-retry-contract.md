# TASK 319: Implement the completion-state retry contract

## Why

Completion evaluation detects repository, policy, or session drift and tells the caller to retry, but neither `Evaluate` nor its CLI callers perform a bounded retry. Concurrent normal activity becomes a manual failure.

## Acceptance

- Completion uses a documented bounded retry count for retryable state drift only.
- Non-retryable policy, proof, Git, task, encoding, and I/O failures return immediately.
- Each attempt owns a fresh coherent capture and never reuses stale report or proof state.
- Persistent mutation exhausts the bound with an exact diagnostic; stable second attempts succeed.

## Sub-Tasks

- [ ] Introduce a typed retryable drift error
- [ ] Choose one owner for bounded retries
- [ ] Add transient and persistent mutation tests
- [ ] Run completion, CLI, task, and race gates

## Notes

- Evidence: `internal/completiongate/gate.go:217-223` and `internal/cli/workflow_cmd.go:699-704,768-773`.

## Deviations

None.
