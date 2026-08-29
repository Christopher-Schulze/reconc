# TASK 416: Bound persistent hook-worker root caching

## Why

The long-lived hook worker caches every request-supplied repository string and resolved root forever. Each distinct key can retain up to 16 KiB plus filesystem identity state.

## Acceptance

- The root cache has a small explicit maximum and deterministic eviction or clear-on-overflow behavior.
- Every hit still revalidates repository identity before reuse.
- Aliases, replaced roots, malformed paths, and eviction cannot return a stale resolved root.
- Allocation tests and benchmarks cover high-cardinality hostile requests and the common single-repository hit path.

## Sub-Tasks

- [ ] Measure practical cache working set and hostile growth.
- [ ] Add the smallest bounded cache policy.
- [ ] Add replacement, alias, eviction, and concurrency regressions.
- [ ] Run focused hook-worker tests and benchmarks.

## Notes

- Verified from finding 98.
- Re-resolution is bounded and existing `ResolvedRepoRoot.Revalidate` remains mandatory on hits.

## Deviations
