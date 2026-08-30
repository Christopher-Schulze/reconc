# TASK 408: Remove action-evaluation hot-path encodings

## Why

Membership evaluation clones operand arrays, pointer summaries walk and size selected subtrees per predicate, runtime-value validation materializes full JSON solely to measure it, and failure handling repeats normalization to compute an ineligible cache identity.

## Acceptance

- Membership iteration uses immutable indexed access without copying operand arrays.
- Exact canonical sizes are reused or computed lazily without materializing JSON solely for limits or unused summaries.
- Failed normalization never repeats the same normalization or digest work.
- Benchmarks cover 256-item membership, 8 MiB values, root-pointer summaries, multi-rule evaluation, and failure paths with lower allocations and identical decisions/traces.

## Sub-Tasks

- [x] Record hot-path allocation and scaling baselines.
- [x] Use existing immutable indexed and non-materializing size APIs.
- [x] Thread prepared size/cache results through success and failure paths.
- [x] Add equivalence tests and run focused benchmarks.

## Notes

- Verified from findings 81, 82, 83, and 87; findings 110 and 111 duplicate 82 and 81 respectively.
- `CanonicalJSONSize` already provides exact size without encoding; any memoization must preserve `Value` immutability and avoid inflating every small value without measured benefit.
- Current-code verification confirmed all four paths: membership used the cloning `Items` accessor; typed request validation encoded values solely to count bytes; every predicate eagerly sized its selected value; and `failEvaluation` called `CacheIdentity`, repeating failed or already completed normalization and digest work.
- Membership now uses bounded immutable indexed reads. Phase and context validation propagate exact `CanonicalJSONSize` results without materializing JSON, while predicate roots lazily memoize only root sizes that can reach a trace.
- Evaluation preparation now derives the cache binding once. Preflight and later failures reuse it; inputs that fail normalization are explicitly ineligible without a second normalization attempt.
- Three-sample baselines and post-change benchmarks measured: 256-item membership 32,768 to 0 B/op; maximum 8 MiB runtime validation 8,388,608 to 0 B/op; 64-rule root evaluation about 1.89 seconds/48.2 MiB to 103 milliseconds/31.4 MiB; late normalization failure about 147 milliseconds/33.6 MiB to 28 milliseconds/13.7 KiB median.
- Verified with the complete uncached action package, allocation regressions, focused one-iteration benchmarks, the Impact Lab inspection-boundary corpus, `make test-fast`, and `git diff --check`.

## Deviations
