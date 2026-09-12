# TASK 540: Reject GNU Parallel option expansions

## Why

F1: GNU Parallel can execute replacement expressions inside options that the command analyzer currently skips.

## Acceptance

- Executable replacement syntax in separate and inline option values fails closed; literal options and input data remain compatible.
- Required validation passes without relaxing existing contracts.

## Sub-Tasks

- [x] Inspect option parsing and the real GNU Parallel reproduction; add parser and runtime controls.
- [x] Reject replacement syntax before skipping option operands, including inline values.
- [x] Run focused regressions and repository gates; flush documentation, archive, commit, and push origin/main.

## Notes

- Christopher explicitly authorized implementation, one commit per TASK, and push to origin/main.
- Initial source: fb4967fbc9c9c1e1e186ddb7f88d9dff0ffb1f67.
- Use isolated test repositories; preserve all existing private historical details.
- The new parser and evaluator cases fail against the original implementation; evidence: `.build/review-repairs/task540-red.log`. The prior real GNU Parallel execution is retained in `fresh-review-parallel-execution.log`.
- Reject replacement-shaped words only before the command/input boundary, including every consumed option operand. Shared dispatcher helpers and literal input handling remain unchanged.
- Validation passed: 36 new parser cases, eight added runtime controls, focused race tests, complete uncached root/template race suites and release trust, vet, Staticcheck, development build, and diff checks. Logs: `.build/review-repairs/task540-*.log`. All 25 original private historical details remain byte-identical.

## Deviations

None.
