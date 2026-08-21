# TASK 214: Deduplicate runtime evidence in linear time

## Why

`appendUnique` performs a linear scan of the accumulated output for each
candidate. Evidence, fresh-file contexts, and merged write collections can
therefore become quadratic at their configured cardinality while requiring
only stable first-seen uniqueness.

## Acceptance

- High-cardinality deduplication uses a bounded membership index plus an output
  slice and preserves exact first-seen order.
- Empty strings, normalized aliases, duplicates across merged sources, and nil
  versus empty output semantics remain unchanged at JSON/report boundaries.
- The index is scoped to one collection build and cannot grow beyond existing
  input/output limits.
- Tests compare old and new results over randomized sequences and cap
  boundaries; benchmarks demonstrate linear scaling.
- Callers that operate on tiny fixed collections are not abstracted
  unnecessarily.

## Sub-Tasks

- [x] Inventory high-cardinality `appendUnique` callsites
- [x] Introduce stable set-plus-slice collection at those sites
- [x] Preserve serialization and order contracts
- [x] Add differential and benchmark tests
- [x] Run runtime and complete gates

## Notes

- All runtime evaluator callsites that build triggered paths, successful
  command evidence, required paths, fresh-file findings, evidence findings,
  composite paths, or synthesized writes now use a collection-local
  set-plus-slice accumulator. It preserves exact first-seen order, empty
  strings, and existing initial duplicate semantics while changing membership
  from a linear scan to O(1). The index is discarded with the collection and
  grows only for values already admitted by the surrounding input/finding
  bounds.
- Randomized differential tests cover duplicate floods and empty values, and
  the benchmark scales from 128 through 8,192 unique paths with linear
  behavior. Runtime tests pass. Fresh final candidate verification in TASK 221
  passed the complete root and portable-template race gates.

## Deviations

None.
