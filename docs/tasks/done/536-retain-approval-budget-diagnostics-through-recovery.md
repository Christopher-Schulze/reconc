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

- [x] Read durable approval record encoding, digest validation, finalization, gateway delivery, and restart paths; reproduce lost acknowledgement after denial exhaustion.
- [x] Persist the secondary terminal diagnostic with the approval record and reconstruct it on recovery using one typed contract; preserve compatibility for older records.
- [x] Keep compare-and-swap protection, terminal status, bounded diagnostics, reservation release, and accounting idempotent.
- [x] Test dropped results, reopened stores, gateway recovery, actual ledger write failure/retry, and competing terminal outcomes; correct tests expecting diagnostic loss.
- [x] Propagate documentation and any necessary format validation, run focused race tests and full gates, archive, commit, and push origin/main.

## Notes

- Starting source: 70117feaa45b85d045670219d598fe9d1f1428e8.
- Source boundaries: internal/actionstate/approval.go; internal/actionstate/budget_types.go; internal/mcpgateway/gateway.go; internal/actionstate/approval_finalize_outcome_test.go.
- Use isolated repository fixtures; never run repository-targeted Reconc commands against the product root.
- Planning details are explicitly tracked for this approved work despite the existing local-task ignore rule.
- Required-ledger reproductions exposed two necessary delivery corrections: shutdown used a different reason from the persisted approval, and the ledger inferred two charged denials while durable state charged only one. Logs: .build/review-repairs/task536-ledger-red.log and task536-accounting-red.log.
- Persist one optional typed denial-accounting value with terminal pre-call approvals, containing actual consumed count and capacity exhaustion. Omission retains legacy record encoding and explicitly means unavailable historical accounting.
- Return actual accounting from the shared ordinary-denial transaction as well; pass it explicitly to ledger projection rather than infer consumption from configured limits. Preserve public ledger formats and all reservation-release deltas.
- Validate diagnostic placement, immutable terminal recovery, dropped acknowledgements, reopened state, partial multi-budget charging, actual ledger filesystem failure/retry, and competing terminal statuses.
- The ordinary-denial integration test exposed premature checkpoint finalization after a blocked pre-decision. Budget continuation now revalidates the retained chain and requires retained request acceptance; existing lifecycle rules still reject actual terminal replay. This necessary shared-path correction preserves the ledger format. It incurs a full retained-chain validation for this exceptional continuation, to be included in TASK 538 performance review.
- Corrected the new multi-budget test to assert consumption by budget identity: durable status ordering follows keyed scope identity, not declaration order.
- Verification: complete actionstate/actionledger/mcpgateway race suites passed, as did explicit expiry recovery, full `make test` (both module race suites and release trust), `make vet`, `make lint`, `make build`, and `git diff --check`. Evidence: `.build/review-repairs/task536-packages.log`, `task536-expiry.log`, `task536-full-test.log`, and `task536-build.log`.
- Gateway tests replace the real ledger file with a directory, retain failed cleanup, restore the file, and verify complete evidence without repeated charges or diagnostics. Checkpoint tests cover warm/cold stores, interleaved calls, and rejection of terminal budget replay.
- Remote Linux CI still has the independently reproduced inherited Gitlink author-fixture failure; its narrow repair and final remote verification belong to TASK 538. No product release was assigned or published.

## Deviations

None.
