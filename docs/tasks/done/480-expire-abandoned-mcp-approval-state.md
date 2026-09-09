# TASK 480: Expire abandoned MCP approval state

## Why

Gateway pending approvals are bounded by count but are removed only through consumption/removal or shutdown. Requests abandoned past the durable approval TTL can retain capacity, reservations and buffered results, preventing later approvals in a long-running gateway.

## Acceptance

- Expired abandoned requests release gateway pending capacity, retained result/argument buffers and owned reservations within a defined bound.
- Durable approval finalization and ledger terminalization remain consistent and at most once under expiry/retry/shutdown races.
- A new request can be admitted after all four existing requests expire without restarting the gateway.
- Cleanup failures are surfaced and retriable without falsely reporting release or discarding the only recoverable state.

## Sub-Tasks

- [x] Trace pending ownership, request expiry, actionstate reconciliation, reservation release and gateway shutdown; reproduce capacity exhaustion after abandoned requests expire.
- [x] Add expiry reconciliation at admission and, where needed for bounded idle retention, through a gateway-owned cancellable lifecycle timer.
- [x] Remove candidates under the pending lock, perform durable finalization outside it with explicit ownership, and retain enough state to handle failures safely.
- [x] Integrate pre-call and post-result paths, legacy retry and modern input-required flow without duplicate finalization.
- [x] Add deterministic clock-driven lifecycle/race tests, document limits and run gateway/actionstate/approval race suites.

## Notes

### Review provenance

- Review finding 11 from the 2026-09-08 source review at `a60196dc2f0954fd4f09d252fa65c709edfe4b01`.
- Status: implemented. The gateway now reconciles durable expiry at startup, before admission, and on a cancellable 30-second sweep; expired in-memory approvals remain bounded cleanup candidates until ledger transitions finish.
- Dependencies: No prerequisite TASK.

### Source anchors

- `internal/mcpgateway/call.go`
- `internal/mcpgateway/approval.go`
- `internal/mcpgateway/gateway.go`
- `internal/mcpgateway/pending_release_test.go`
- `internal/actionstate/budget_types.go`
- `internal/actionapproval/types.go`

### Technical design

- Read expiry from the issued approval contract; do not start a second independent TTL calculation that drifts from durable state.
- Maintain established lock ordering with transitionMu and pendingMu. Never perform unbounded disk operations while holding the pending map mutex.
- Reuse terminalContext and existing release/finalization paths after inspecting error contracts.
- Destroy retained references after finalization according to existing data ownership; do not promise secure memory zeroization for arbitrary Go copies.
- Expiry claims match durable `ApprovalEvidence.RequestID` values. Pre-call expiry records approval plus denied budget evidence; post-result expiry settles dispatched reservations, records settled or indeterminate budget evidence, and records withheld delivery. Cleanup flags make retries at-most-once per transition.

### Verification and completion

- Fill the capacity with real issued approvals, advance a controlled clock beyond TTL, and verify a fifth request succeeds while old receipts are rejected.
- Race expiry with successful consume, explicit removal, shutdown and ledger failure; assert one terminal outcome and no leaked reservation. The package race suite and shutdown ordering tests cover these interleavings; cleanup retains failed candidates for retry.
- Cover post-result buffering and cancellation while cleanup is running. Deterministic pre-call and post-result raw MCP tests cover capacity release, stale receipt rejection, reservation settlement, and new admission.
- Verification: `go test ./internal/mcpgateway -count=1`, `go test -race ./internal/mcpgateway -count=1`, `make test-fast`, `make test`, `make vet`, `make lint`, and `git diff --check` passed. A separate `go test ./... -count=1` run had one legacy elicitation timeout under concurrent repository load; the isolated package and the required bounded-parallel/race gates passed.
- Keep changes scoped, preserve unrelated work, flush current behavior into `docs/documentation.md` where needed, then archive this detail and create the single TASK commit. Never push or change the product version without explicit authorization.
- Never run repository-targeted Reconc commands against this product root; integration validation belongs in isolated temporary repositories and `make self-host`.

## Deviations

None planned.
