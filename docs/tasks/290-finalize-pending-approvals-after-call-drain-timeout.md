# TASK 290: Finalize pending approvals after call-drain timeout

## Why

`Gateway.Close` runs `shutdownPending` only when `waitForCalls` succeeds. One call that outlives `ShutdownTimeout` can therefore prevent unrelated pending approvals from reaching a terminal state.

## Acceptance

- Pending approvals receive a bounded finalization attempt even when active calls fail to drain.
- In-flight calls observe gateway cancellation in addition to their SDK request context.
- Shutdown never waits beyond the documented bound and reports both drain and finalization failures.
- Tests cover a stuck downstream call plus independent pending pre- and post-result approvals.

## Sub-Tasks

- [ ] Define the combined call and gateway cancellation boundary
- [ ] Decouple pending finalization from successful call draining
- [ ] Add stuck-call and mixed-pending shutdown regressions
- [ ] Run MCP race, leak, and lifecycle gates

## Notes

- Evidence: `internal/mcpgateway/call.go:106-107` and `internal/mcpgateway/gateway.go:568-598`.

## Deviations

None.
