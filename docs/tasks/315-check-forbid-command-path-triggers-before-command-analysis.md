# TASK 315: Check forbid-command path triggers before command analysis

## Why

Top-level `forbid_command` parses and matches all observed commands before evaluating `when_paths`. Rules irrelevant to the current write set still pay the complete shell-analysis cost.

## Acceptance

- Non-matching `when_paths` return before any command parsing or matching.
- Rules without path constraints and rules with matching paths retain exact forbidden-command, uncertainty, and violation ordering semantics.
- Composite rule behavior remains unchanged unless its own path contract proves the same optimization.
- Allocation and timing regressions cover many irrelevant rules and maximum commands.

## Sub-Tasks

- [ ] Reorder only independent trigger evaluation
- [ ] Prove error and diagnostic ordering remains acceptable
- [ ] Add parser-call-count and behavior tests
- [ ] Run runtime, shell, fuzz, and benchmark gates

## Notes

- Evidence: `internal/runtime/evaluator_rules.go:598-617`; require-command checks paths first at `:662-669`.

## Deviations

None.
