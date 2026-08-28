# TASK 312: Use canonical size for budget arguments

## Why

Budget evaluation materializes complete canonical argument JSON only to measure its length, although immutable `action.Value` already exposes exact allocation-free `CanonicalJSONSize`.

## Acceptance

- Argument-byte budgets use the exact canonical size primitive without materializing JSON.
- Nil, depth, overflow, and invalid-value errors remain identical in classification.
- Budget snapshots and ledger byte counts retain exact parity with canonical encoding.
- Allocation regression, budget, gateway, and fuzz tests pass.

## Sub-Tasks

- [x] Replace the size-only marshal call
- [x] Prove parity across every Value kind and boundary
- [x] Add zero-allocation budget-size coverage
- [x] Run action, state, ledger, and gateway gates

## Notes

- Evidence: `internal/action/budget_eval.go:306-324` and `internal/action/value.go:378-382`.
- Both the shared multi-budget measurement and the standalone `RequiredBudgetUsage` fallback must use `CanonicalJSONSize`; otherwise identical budget semantics retain two size implementations.
- Replace the optional size pointer with explicit value/presence returns so the hot measurement path can stay allocation-free.
- Canonical-kind/depth/error parity, two action fuzz targets, Action/State/Ledger/Gateway tests, and the shared-size benchmark passed at `0 B/op` and `0 allocs/op`; `make test`, `make vet`, Staticcheck v0.8.1, and `make self-host` are green.

## Deviations

None.
