# TASK 290: Finalize pending approvals after call-drain timeout

## Why

`Gateway.Close` runs `shutdownPending` only when `waitForCalls` succeeds. One call that outlives `ShutdownTimeout` can therefore prevent unrelated pending approvals from reaching a terminal state.

## Acceptance

- Pending approvals receive a bounded finalization attempt even when active calls fail to drain.
- In-flight calls observe gateway cancellation in addition to their SDK request context.
- Shutdown never waits beyond the documented bound and reports both drain and finalization failures.
- Tests cover a stuck downstream call plus independent pending pre- and post-result approvals.

## Sub-Tasks

- [x] Define the combined call and gateway cancellation boundary
- [x] Decouple pending finalization from successful call draining
- [x] Add stuck-call and mixed-pending shutdown regressions
- [x] Run MCP race, leak, and lifecycle gates

## Notes

- Every new or resumed MCP call now observes request cancellation, the configured call deadline, and gateway cancellation through one owned context.
- Shutdown finalizes detached pending approvals before the bounded call drain and joins errors from both stages instead of making finalization conditional on a successful drain.
- Regression coverage includes request and gateway cancellation, simultaneous finalization and drain failures, one tracked stuck downstream call, and independent pending pre-call and post-result approvals.
- Verification: `go test -count=1 ./internal/mcpgateway`; `go test -race -count=1 ./internal/mcpgateway`; `make test`; `make vet`; `make lint`; `make reference-docs-check`.

## Deviations

None.
