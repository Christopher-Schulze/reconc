# TASK 504: Retry MCP shutdown finalization without losing pending state

## Why

Gateway shutdown detaches pending approvals and releases them even when durable approval finalization or ledger terminalization fails. The first `Close` is guarded by `sync.Once`, so a transient failure can strand the durable approval and its reservation with no retry path.

## Acceptance

- Every pending approval keeps a bounded retry record until durable finalization and its ledger terminal state both succeed.
- Shutdown retries are ordered, at-most-once per completed ledger stage, and preserve pending capacity and reservation truth while incomplete.
- Sensitive wire buffers are zeroed while the minimal structured retry state remains available.
- A later `Close` retries retained shutdown work and releases the identity-key lease only after cleanup succeeds.
- Existing expiry cleanup behavior and close idempotence remain intact, with deterministic failure/retry tests.

## Sub-Tasks

- [x] Model shutdown cleanup stages separately from expiry cleanup and retain failed work.
- [x] Add retry-on-later-Close and lease-lifetime handling.
- [x] Update shutdown tests and gateway lifecycle documentation.
- [x] Run gateway focused/race/full gates, archive this task, commit and push.

## Notes

- Discovered during the read-only reality audit of TASK 480.
- Verification: focused shutdown tests, `go test ./internal/mcpgateway -count=1`, `go test -race ./internal/mcpgateway -count=1`, `make test-fast`, `make vet`, `make lint`, and `git diff --check` passed.

## Deviations

None planned.
