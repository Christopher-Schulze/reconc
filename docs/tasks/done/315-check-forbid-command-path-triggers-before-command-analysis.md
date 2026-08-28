# TASK 315: Check forbid-command path triggers before command analysis

## Why

Top-level `forbid_command` parses and matches all observed commands before evaluating `when_paths`. Rules irrelevant to the current write set still pay the complete shell-analysis cost.

## Acceptance

- Non-matching `when_paths` return before any command parsing or matching.
- Rules without path constraints and rules with matching paths retain exact forbidden-command, uncertainty, and violation ordering semantics.
- Composite rule behavior remains unchanged unless its own path contract proves the same optimization.
- Allocation and timing regressions cover many irrelevant rules and maximum commands.

## Sub-Tasks

- [x] Reorder only independent trigger evaluation
- [x] Prove error and diagnostic ordering remains acceptable
- [x] Add parser-call-count and behavior tests
- [x] Run runtime, shell, fuzz, and benchmark gates

## Notes

- Evidence: `internal/runtime/evaluator_rules.go:598-617`; require-command checks paths first at `:662-669`.
- Top-level `forbid_command` now evaluates `when_paths` before invoking the cached shell matcher. Composite rules remain unchanged because `compositeSetup` already resolves their own path contexts before sub-check evaluation.
- The shell equivalence fuzz harness skips inputs the reference parser classifies as syntactically unparsable or whose outer quote-removal pass changes the eval body; it still fails on dynamic, boundedness, or evaluation mismatches. This keeps the shell gate failable without treating unsupported control characters or line-ending normalization as valid equivalence cases.
- Apple M1 irrelevant-rule benchmark, 128 maximum commands and 128 path-constrained forbid rules, three 50-iteration samples: 1,244,428 ns/op, 7,022,865 ns/op, and 1,197,157 ns/op; the middle sample was a system outlier, with no performance claim taken from it.
- Verification: runtime and shell unit/race tests, parser-call-count regression, runtime and shell fuzz targets, focused benchmark, `make test-fast`, `make vet`, Staticcheck v0.8.1, and `make self-host` all passed.

## Deviations

None.
