# TASK 409: Correct action indeterminate classification and limits

## Why

A scalar target whose type differs from an `in` operand list is agent-controlled input, but evaluation labels it `internal_invariant` and bypasses the rule's `OnIndeterminate` policy. The `max_result_bytes` validation message also says the value must be positive while zero is the struct's absent/default representation.

## Acceptance

- Agent-controlled target type mismatches produce `condition_indeterminate` and honor the configured `OnIndeterminate` outcome.
- `internal_invariant` remains reserved for impossible compiled-plan corruption.
- The `max_result_bytes` zero/absent contract is unambiguous in validation, schema, docs, and budget charging.
- Table tests cover cross-kind targets, corrupt operands, every `OnIndeterminate` mode, zero, positive bounds, and overflow.

## Sub-Tasks

- [ ] Separate target mismatch from operand invariant failure.
- [ ] Choose and propagate one zero/absent result-limit contract.
- [ ] Add evaluator, schema, and plan regressions.
- [ ] Run focused action tests.

## Notes

- Verified from findings 85 and 88; finding 109 duplicates finding 85.
- Compiled operand lists are already guaranteed same-kind scalar; only the target mismatch is normal indeterminate input.

## Deviations
