# TASK 327: Reuse shell parser state safely

## Why

Shell command inspection constructs a new `mvdan` Bash parser for each direct and recursive analysis. Repeated commands across forbid, proof, redirect, and hook paths repay parser setup and AST allocation.

## Acceptance

- Benchmarks separate parser construction, parse, AST walk, and caller duplication costs.
- Safe parser reuse or bounded command-result memoization preserves source positions, recursion budgets, uncertainty reasons, and concurrency safety.
- No AST or sensitive command bytes survive beyond the documented cache lifetime.
- Shell fuzz, race, wrapper, redirect, and benchmark tests pass.

## Sub-Tasks

- [x] Profile parser construction and repeated-command frequency
- [x] Verify the parser's actual reset and concurrency contract
- [x] Implement the smallest safe reuse boundary
- [x] Add aliasing, clearing, race, and allocation tests

## Notes

- Evidence: `internal/shellcommand/shellcommand.go:165-237` and repeated runtime callers. No pooling is accepted without reading and testing the dependency's real API contract.
- `mvdan.cc/sh/v3/syntax.Parser.Parse` is reusable only after a call completes,
  never concurrently. A global pool was rejected because the dependency keeps
  source buffers and AST allocation batches internally.
- One analysis-local parser now serves nested invocation recursion and the
  redirect validation reparse; concurrent top-level callers receive separate
  states. Benchmarks cover construction, parse, AST walk, and combined caller
  costs. Reuse preserves earlier ASTs and the full shell/fuzz contract.

## Deviations

None.
