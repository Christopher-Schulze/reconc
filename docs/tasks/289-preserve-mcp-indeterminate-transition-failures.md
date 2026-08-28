# TASK 289: Preserve MCP indeterminate-transition failures

## Why

MCP dispatch and approval error paths discard failures from `MarkIndeterminate` or `markIndeterminate`. A ledger failure can therefore hide that the reservation remained dispatched or otherwise non-terminal.

## Acceptance

- Every indeterminate transition result is checked and its error is joined with the triggering lifecycle failure.
- Ledger records never claim a state version that was not successfully committed.
- Failure diagnostics remain bounded and expose the exact unresolved reservation state.
- Fault-injection tests cover dispatch, budget-ledger, pre-approval, and post-approval failures.

## Sub-Tasks

- [ ] Map every ignored indeterminate transition
- [ ] Define one fail-closed transition helper and caller contract
- [ ] Add state-version and ledger-order regressions
- [ ] Run MCP, action-state, ledger, and race gates

## Notes

- Evidence: `internal/mcpgateway/call.go:394-405` and `internal/mcpgateway/approval.go:208-225,472-478`; the retry-capable reference is `internal/mcpgateway/result.go:266-280`.

## Deviations

None.
