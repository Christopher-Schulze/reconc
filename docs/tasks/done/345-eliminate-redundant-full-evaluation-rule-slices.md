# TASK 345: Eliminate redundant full-evaluation rule slices

## Why

Full runtime evaluation materializes all rule indexes and then a second slice of rule pointers even though the compiled plan already owns the rules in evaluation order. This creates avoidable per-hook allocations.

## Acceptance

- Full evaluation iterates compiled rules without allocating redundant index or pointer slices.
- Filtered evaluation retains its existing selection and ordering behavior.
- Tests cover full and filtered rule evaluation equivalence.
- Benchmarks demonstrate the removed allocations.

## Sub-Tasks

- [x] Separate the full-plan iteration path from filtered index selection.
- [x] Remove the redundant full-evaluation slices.
- [x] Add equivalence and allocation regressions.
- [x] Run focused runtime tests and benchmarks.

## Notes

- Verified from OMP session `01a04cc5-6a01-7526-95bb-b2715553daf3`, finding #90.
- Current-code verification confirmed `internal/runtime/evaluator_core.go` built a complete integer-index slice and then a complete rule-pointer slice for every unfiltered evaluation.
- Full evaluation now uses the compiled rule slice directly. Filtered and pre-command evaluation retain their existing stable index selection; batched script handling consumes the same selection without materializing rule pointers.
- Regression coverage proves an all-kind filtered evaluation is report-identical to full evaluation, while subset and empty filters retain their existing behavior and ordering.
- `BenchmarkCheckMixedRuleset` dropped from 659 to 657 allocations per operation and from 118660-118814 to 118634-118709 bytes per operation. Post-change latency measured 444777-481401 ns per operation versus a noisy 470400-645511 ns baseline, so no latency improvement is claimed.
- Verification passed: focused runtime tests, `make test-fast`, `make test`, `make vet`, `make lint`, the relevant benchmark, and `git diff --check`. Windows-specific tests were maintained but not executed locally, per the queue execution contract.
- The first publication-audit run exposed a credential-pattern false positive stored in the local TASK 344 commit. Its archived prose was corrected and the unpushed TASK 344 commit was amended before rerunning the complete gate successfully.

## Deviations

None.
