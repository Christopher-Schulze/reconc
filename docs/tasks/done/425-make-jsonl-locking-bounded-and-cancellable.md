# TASK 425: Make JSONL locking bounded and cancellable

## Why

Default JSONL append uses `context.Background` with a zero lock timeout, archive reads can retry for roughly two seconds under the writer lock, and enforce/audit maintenance cannot accept caller cancellation.

## Acceptance

- Every production JSONL lock acquisition has an explicit bounded deadline or caller context.
- Archive stabilization retries stop promptly on cancellation and never sleep while holding an unrelated lock longer than the documented budget.
- Public API changes update all callers atomically and retain existing default behavior through an explicit policy.
- Deterministic tests cover lock contention, cancellation, archive churn, and deadline expiry.

## Sub-Tasks

- [x] Inventory JSONL append, tail, enforce, audit, and retention lock call chains.
- [x] Thread contexts and explicit timeouts through production entrypoints.
- [x] Shorten or restructure archive stabilization without accepting torn archives.
- [x] Run focused JSONL and audit tests.

## Notes

- Verified from findings 105, 149, and 159.
- Reverification confirmed that the generic default layout had no finite lock timeout, audit and retention compatibility entry points used background contexts, and archive stabilization could wait for 399 five-millisecond delays.
- Generic compatibility entry points now use the shared ten-second lock budget. Context entry points propagate cancellation through append, recovery, snapshot reads, enforcement, audit serialization, audit verification/export, and retention maintenance.
- Archive stabilization now performs at most 20 reads with 19 five-millisecond waits and exits immediately on cancellation. Exact legacy default-layout journal identities remain recoverable after the timeout policy change.
- Writer and retention now share one 30-second run-decision layout identity; the previous zero-timeout retention layout could reject a writer journal as foreign.
- Focused JSONL, audit, retention, repository-control, and run-decision caller tests passed. The complete audit package emitted no failure before the operator's short-run ceiling and was interrupted after 70.25 seconds; queue-wide long-running gates remain deferred to the final pass.

## Deviations
