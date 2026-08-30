# TASK 442: Validate budget selector and tool identities

## Why

Budget preflight ignores `ToolContractDigests` when proving a selector can match a declared tool, while `BudgetContract` reports an unknown request tool as a successful zero-tool/zero-budget result. Both paths can make an impossible or drifted budget contract appear absent.

## Acceptance

- Compile-time selector/tool admission preserves tool-contract digests as runtime exact constraints while proving every declaration-owned dimension.
- Runtime budget lookup returns a typed fail-closed error for an undeclared exact tool identity.
- Legitimate declared tools with no matching budgets still return a successful empty budget list.
- Tests cover digest-only selectors, mixed dimensions, unknown tools, no-budget tools, wildcard declarations, and caller error mapping.

## Sub-Tasks

- [x] Align compile-time and runtime exact-tool matching dimensions.
- [x] Define and propagate the unknown-tool error through gateway/action-state callers.
- [x] Add plan, budget, gateway, and state regressions.
- [x] Run focused action and action-state tests.

## Notes

- Verified from findings 175 and 177.
- Revalidation corrected finding 175: `Tool` declarations carry no MCP contract digest, so comparing `ToolContractDigests` inside `selectorCanMatchTool` would reject valid runtime-conditional budgets. Compile-time admission must prove only declared dimensions; runtime `selectorMatches` already enforces the digest exactly.
- Finding 177 remains valid: `BudgetContract` returned a zero tool and empty budgets with no error when exact and wildcard lookup both missed.

## Deviations
