# TASK 425: Make JSONL locking bounded and cancellable

## Why

Default JSONL append uses `context.Background` with a zero lock timeout, archive reads can retry for roughly two seconds under the writer lock, and enforce/audit maintenance cannot accept caller cancellation.

## Acceptance

- Every production JSONL lock acquisition has an explicit bounded deadline or caller context.
- Archive stabilization retries stop promptly on cancellation and never sleep while holding an unrelated lock longer than the documented budget.
- Public API changes update all callers atomically and retain existing default behavior through an explicit policy.
- Deterministic tests cover lock contention, cancellation, archive churn, and deadline expiry.

## Sub-Tasks

- [ ] Inventory JSONL append, tail, enforce, audit, and retention lock call chains.
- [ ] Thread contexts and explicit timeouts through production entrypoints.
- [ ] Shorten or restructure archive stabilization without accepting torn archives.
- [ ] Run focused JSONL and audit tests.

## Notes

- Verified from findings 105, 149, and 159.

## Deviations
