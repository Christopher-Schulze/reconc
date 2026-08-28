# TASK 291: Continue MCP shutdown finalization after individual errors

## Why

`shutdownPending` clears the complete pending map but returns on the first approval or post-approval finalization error. Every later approval is released from memory without a terminal state transition attempt.

## Acceptance

- Shutdown attempts every detached pending approval in deterministic order.
- Per-approval failures are accumulated with call identity and phase without leaking sensitive payloads.
- All detached records release sensitive buffers exactly once.
- Tests inject failures at the first, middle, and last pending approval and prove later entries are still finalized.

## Sub-Tasks

- [ ] Convert shutdown finalization to aggregate-error processing
- [ ] Preserve exact pre- and post-result terminal semantics
- [ ] Add multi-error and release-ownership tests
- [ ] Run MCP lifecycle and race gates

## Notes

- Evidence: `internal/mcpgateway/gateway.go:646-677`.

## Deviations

None.
