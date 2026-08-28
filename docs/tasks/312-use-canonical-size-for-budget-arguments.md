# TASK 312: Use canonical size for budget arguments

## Why

Budget evaluation materializes complete canonical argument JSON only to measure its length, although immutable `action.Value` already exposes exact allocation-free `CanonicalJSONSize`.

## Acceptance

- Argument-byte budgets use the exact canonical size primitive without materializing JSON.
- Nil, depth, overflow, and invalid-value errors remain identical in classification.
- Budget snapshots and ledger byte counts retain exact parity with canonical encoding.
- Allocation regression, budget, gateway, and fuzz tests pass.

## Sub-Tasks

- [ ] Replace the size-only marshal call
- [ ] Prove parity across every Value kind and boundary
- [ ] Add zero-allocation budget-size coverage
- [ ] Run action, state, ledger, and gateway gates

## Notes

- Evidence: `internal/action/budget_eval.go:306-324` and `internal/action/value.go:378-382`.

## Deviations

None.
