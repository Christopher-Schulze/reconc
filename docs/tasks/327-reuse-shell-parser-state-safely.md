# TASK 327: Reuse shell parser state safely

## Why

Shell command inspection constructs a new `mvdan` Bash parser for each direct and recursive analysis. Repeated commands across forbid, proof, redirect, and hook paths repay parser setup and AST allocation.

## Acceptance

- Benchmarks separate parser construction, parse, AST walk, and caller duplication costs.
- Safe parser reuse or bounded command-result memoization preserves source positions, recursion budgets, uncertainty reasons, and concurrency safety.
- No AST or sensitive command bytes survive beyond the documented cache lifetime.
- Shell fuzz, race, wrapper, redirect, and benchmark tests pass.

## Sub-Tasks

- [ ] Profile parser construction and repeated-command frequency
- [ ] Verify the parser's actual reset and concurrency contract
- [ ] Implement the smallest safe reuse boundary
- [ ] Add aliasing, clearing, race, and allocation tests

## Notes

- Evidence: `internal/shellcommand/shellcommand.go:165-237` and repeated runtime callers. No pooling is accepted without reading and testing the dependency's real API contract.

## Deviations

None.
