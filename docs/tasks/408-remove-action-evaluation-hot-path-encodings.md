# TASK 408: Remove action-evaluation hot-path encodings

## Why

Membership evaluation clones operand arrays, pointer summaries walk and size selected subtrees per predicate, runtime-value validation materializes full JSON solely to measure it, and failure handling repeats normalization to compute an ineligible cache identity.

## Acceptance

- Membership iteration uses immutable indexed access without copying operand arrays.
- Exact canonical sizes are reused or computed lazily without materializing JSON solely for limits or unused summaries.
- Failed normalization never repeats the same normalization or digest work.
- Benchmarks cover 256-item membership, 8 MiB values, root-pointer summaries, multi-rule evaluation, and failure paths with lower allocations and identical decisions/traces.

## Sub-Tasks

- [ ] Record hot-path allocation and scaling baselines.
- [ ] Use existing immutable indexed and non-materializing size APIs.
- [ ] Thread prepared size/cache results through success and failure paths.
- [ ] Add equivalence tests and run focused benchmarks.

## Notes

- Verified from findings 81, 82, 83, and 87; findings 110 and 111 duplicate 82 and 81 respectively.
- `CanonicalJSONSize` already provides exact size without encoding; any memoization must preserve `Value` immutability and avoid inflating every small value without measured benefit.

## Deviations
