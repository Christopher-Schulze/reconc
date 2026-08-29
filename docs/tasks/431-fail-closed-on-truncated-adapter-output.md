# TASK 431: Fail closed on truncated adapter output

## Why

Generated OpenCode and Kilo adapters record output truncation but do not include it in their blocking-failure predicate. A truncated deny or error body can fail JSON parsing and be treated as allow.

## Acceptance

- Truncated pre-tool and Stop output always takes the platform's fail-closed response path.
- Post/observation routes retain their documented failure policy.
- Exact-limit output remains accepted; limit-plus-one is deterministically rejected.
- Generated-reference and Bun contract tests cover stdout, stderr, mixed output, UTF-8 boundaries, timeout, and nonzero exit.

## Sub-Tasks

- [ ] Align generated `readCombined` status with route failure policy.
- [ ] Add exact-limit and adversarial truncation fixtures for both adapters.
- [ ] Regenerate expected references through existing tooling.
- [ ] Run focused generated-adapter tests.

## Notes

- Verified from finding 114; OMP and Pi already treat truncation as blocking and provide the parity reference.

## Deviations
