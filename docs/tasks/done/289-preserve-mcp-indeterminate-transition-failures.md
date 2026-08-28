# TASK 289: Preserve MCP indeterminate-transition failures

## Why

MCP dispatch and approval error paths discard failures from `MarkIndeterminate` or `markIndeterminate`. A ledger failure can therefore hide that the reservation remained dispatched or otherwise non-terminal.

## Acceptance

- Every indeterminate transition result is checked and its error is joined with the triggering lifecycle failure.
- Ledger records never claim a state version that was not successfully committed.
- Failure diagnostics remain bounded and expose the exact unresolved reservation state.
- Fault-injection tests cover dispatch, budget-ledger, pre-approval, and post-approval failures.

## Sub-Tasks

- [x] Map every ignored indeterminate transition
- [x] Define one fail-closed transition helper and caller contract
- [x] Add state-version and ledger-order regressions
- [x] Run MCP, action-state, ledger, and race gates

## Notes

- All dispatch and post-result approval ledger failures now use one checked transition contract. A successfully persisted state version remains usable even if transition finalization reports an error; no ledger event is emitted for an unconfirmed version.
- Malformed and unknown downstream failures share the same fail-closed ordering: state transition, downstream evidence, then budget evidence.
- Regression coverage includes stale-version retry, unavailable state, normal and approved pre-call dispatch, post-result approval reservation and consumption, bounded diagnostics, and false-ledger-version prevention.
- Verification: `go test -count=1 ./internal/mcpgateway`; `go test -race -count=1 ./internal/mcpgateway ./internal/actionstate ./internal/actionledger`; `make test`; `make vet`; `make lint`.

## Deviations

None.
