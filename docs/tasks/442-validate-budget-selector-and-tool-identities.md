# TASK 442: Validate budget selector and tool identities

## Why

Budget preflight ignores `ToolContractDigests` when proving a selector can match a declared tool, while `BudgetContract` reports an unknown request tool as a successful zero-tool/zero-budget result. Both paths can make an impossible or drifted budget contract appear absent.

## Acceptance

- Compile-time selector/tool matching includes the exact tool-contract digest dimension.
- Runtime budget lookup returns a typed fail-closed error for an undeclared exact tool identity.
- Legitimate declared tools with no matching budgets still return a successful empty budget list.
- Tests cover digest-only selectors, mixed dimensions, unknown tools, no-budget tools, wildcard declarations, and caller error mapping.

## Sub-Tasks

- [ ] Align compile-time and runtime exact-tool matching dimensions.
- [ ] Define and propagate the unknown-tool error through gateway/action-state callers.
- [ ] Add plan, budget, gateway, and state regressions.
- [ ] Run focused action and action-state tests.

## Notes

- Verified from findings 175 and 177.

## Deviations
