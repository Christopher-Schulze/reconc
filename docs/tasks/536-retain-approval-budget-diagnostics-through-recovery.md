# TASK 536: Retain approval budget diagnostics through recovery

## Why

Approval finalization can persist cancellation with exhausted denial capacity. Recovery reconstructs the terminal result but loses the secondary diagnostic when the original result was not acknowledged.

User-approved review item: R5. Execute on main; commit and push after completion. Product tags and releases remain outside this task.

## Acceptance

- Persisted terminal recovery retains the original budget diagnostic without recharging denial or approval budgets.
- Cleanup can finish after lost acknowledgements and retries; terminal results remain immutable.
- Pre-persist failures cannot be misclassified as committed outcomes; legacy records remain readable.
- Deterministic persistence/recovery/concurrency regressions and required gates pass.

## Sub-Tasks

- [ ] Read durable approval record encoding, digest validation, finalization, gateway delivery, and restart paths; reproduce lost acknowledgement after denial exhaustion.
- [ ] Persist the secondary terminal diagnostic with the approval record and reconstruct it on recovery using one typed contract; preserve compatibility for older records.
- [ ] Keep compare-and-swap protection, terminal status, bounded diagnostics, reservation release, and accounting idempotent.
- [ ] Test dropped results, reopened stores, gateway recovery, actual ledger write failure/retry, and competing terminal outcomes; correct tests expecting diagnostic loss.
- [ ] Propagate documentation and any necessary format validation, run focused race tests and full gates, archive, commit, and push origin/main.

## Notes

- Starting source: 70117feaa45b85d045670219d598fe9d1f1428e8.
- Source boundaries: internal/actionstate/approval.go; internal/actionstate/budget_types.go; internal/mcpgateway/gateway.go; internal/actionstate/approval_finalize_outcome_test.go.
- Use isolated repository fixtures; never run repository-targeted Reconc commands against the product root.
- Planning details are explicitly tracked for this approved work despite the existing local-task ignore rule.

## Deviations

None.
