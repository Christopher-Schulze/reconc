# TASK 291: Continue MCP shutdown finalization after individual errors

## Why

`shutdownPending` clears the complete pending map but returns on the first approval or post-approval finalization error. Every later approval is released from memory without a terminal state transition attempt.

## Acceptance

- Shutdown attempts every detached pending approval in deterministic order.
- Per-approval failures are accumulated with call identity and phase without leaking sensitive payloads.
- All detached records release sensitive buffers exactly once.
- Tests inject failures at the first, middle, and last pending approval and prove later entries are still finalized.

## Sub-Tasks

- [x] Convert shutdown finalization to aggregate-error processing
- [x] Preserve exact pre- and post-result terminal semantics
- [x] Add multi-error and release-ownership tests
- [x] Run MCP lifecycle and race gates

## Notes

- Detached approvals are sorted by call identity, phase, and sealed request state; each receives a finalization attempt even after earlier failures.
- Aggregated errors expose only the safe call identity and phase plus the typed cause. Request state and approval payloads remain private.
- Pre-call and post-result paths retain their existing terminalization behavior, and every detached record is released by one batch-owned cleanup pass.
- Regression coverage injects first, middle, last, and multiple ordered failures across pre-call and post-result approvals, proves unaffected entries terminalize, verifies deterministic error order, and checks owned buffers are cleared.
- Verification: `go test -count=1 ./internal/mcpgateway`; `go test -race -count=1 ./internal/mcpgateway`; `make test`; `make vet`; `make lint`.

## Deviations

None.
