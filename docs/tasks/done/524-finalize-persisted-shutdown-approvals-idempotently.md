# TASK 524: Finalize persisted shutdown approvals idempotently

## Why

Approval finalization can commit a terminal approval record and reservation
transition, then return both the persisted result and a `BudgetExhausted` error
when denial-count capacity is exhausted. Gateway shutdown cleanup currently
records the result and advances to terminal cleanup only when the error is nil.
Retrying the same cleanup with its issuance version then encounters the already
advanced state as stale, leaving pending shutdown cleanup, leases, and terminal
delivery unable to finish.

This task implements audit candidate C220. The repair must distinguish a failed
mutation from a successfully persisted terminal transition accompanied by a
secondary diagnostic; treating every error as either committed or uncommitted
would corrupt approval and budget semantics.

## Acceptance

- Define one typed finalization outcome that states whether a terminal approval
  transition was persisted, exposes its resulting state version and record,
  and separately carries any terminal reservation or denial-capacity condition.
- Preserve compare-and-swap protection: a genuinely stale issuance version,
  mismatched request, missing reservation, corrupt state, failed signature, or
  failed write must never be treated as a successful finalization.
- When terminal state was persisted, gateway shutdown must retain that exact
  result, mark the finalization phase complete, and continue terminal delivery,
  pending-entry removal, lease release, and shutdown draining exactly once.
- Retrying cleanup after cancellation, delivery failure, or process-level
  interruption must recover the persisted terminal result without attempting a
  second state transition or failing permanently on the old issuance version.
- Preserve denial accounting, reservation accounting, maximum approval-record
  bounds, cancellation reason, terminal status, timestamps, ordering, and state
  generation monotonicity.
- Concurrent approve, deny, cancel, timeout, disconnect, and shutdown paths must
  have one linearizable winner. Every loser receives the existing terminal
  result or a precise conflict; no path may reopen or double-consume a request.
- Pending cleanup remains retained only while real terminal delivery or lease
  cleanup is incomplete. A secondary budget error alone must not strand it.
- Do not swallow `BudgetExhausted`; expose it through the established bounded
  diagnostic/status channel after durable cleanup is safe.
- Add deterministic tests that force denial capacity exhaustion at the exact
  finalization boundary, then exercise shutdown retry, delivery retry, lease
  closure, gateway restart/recovery, and concurrent terminalization.
- Add failure injection for pre-write, post-write/result-return, stale version,
  terminal delivery, and cleanup persistence so tests prove which side effects
  did and did not occur.
- Pass action-state and MCP gateway tests under the race detector, repeat the
  ordering regression enough to expose synchronization faults, then pass all
  required repository gates.

## Sub-Tasks

- [x] Read every approval finalization signature and caller, terminal reservation/accounting path, gateway pending-cleanup lifecycle, lease owner, persistence boundary, and recovery path.
- [x] Specify the persisted-versus-unpersisted outcome algebra and exact retry/idempotency rules without weakening optimistic concurrency.
- [x] Implement typed terminal-result recovery in action state and consume it in gateway shutdown cleanup through one canonical path.
- [x] Ensure delivery, pending-map removal, lease release, and shutdown draining are individually idempotent and ordered after durable terminal state.
- [x] Add deterministic capacity-exhaustion, stale-version, retry, restart, failure-injection, and concurrency regressions with no sleeps or always-green assertions.
- [x] Audit bounded diagnostics and observability so secondary errors remain visible without preventing cleanup.
- [x] Update approval lifecycle and shutdown documentation for the finalized outcome and retry contract.
- [x] Run focused race/repetition tests and every required repository gate; re-read all modified files before archival.

## Notes

### Implementation

- `FinalizeApproval` returns `ApprovalFinalizeOutcome`. `Persisted` plus
  `TerminalResult()` are the only success signal for durable terminal state.
- Pending records still require the sealed issuance version. Terminal records
  are recovered by request-state identity even when the caller still holds the
  pre-write issuance version.
- `DenialCountCapacityExhausted` is a post-write diagnostic. Gateway shutdown
  retains the terminal result, finishes ledger delivery, then writes the
  diagnostic. A later `Close` does not attempt a second state transition.
- Startup expiry reconciliation no longer fails the process solely because
  denial-count capacity is exhausted after a successful persist.

### Final verification

- Focused `go test ./internal/actionstate ./internal/mcpgateway -count=1` passed.
- Focused race/repetition runs for the new finalization and shutdown tests passed.
- `make test-fast`, `make vet`, `make lint`, `make build`, `make coverage`,
  `make self-host`, and `make publication-audit` passed. The complete `make test`
  race suite passed; release-trust initially failed on a leftover numeric
  coverage sentence in the TASK 525 archive, which was removed, after which
  `./scripts/tests/release-trust.sh` passed.
- `gofmt` is clean and `git diff --check` passes.

### Required state-machine properties

- Durable state precedes acknowledgement.
- Exactly one terminal status exists per approval request.
- Reservation release and denial accounting happen at most once.
- A returned error must not erase knowledge of a committed result.
- Cleanup phases may resume independently but never run out of order.
- A gateway restart can infer durable truth without trusting stale in-memory
  flags.

### Verification matrix

- Approved, denied, cancelled, expired, shutdown-cancelled, and disconnected.
- Denial count below capacity, exactly at capacity, and already exhausted.
- State write fails before publication; write succeeds and a secondary error is
  returned; retry uses old or current generation.
- One and many concurrent finalizers; cancellation races with response delivery.
- Delivery succeeds/fails; lease release succeeds/fails; process recovery reads
  terminal state; duplicate cleanup remains harmless.

## Deviations

None.
