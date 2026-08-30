# TASK 416: Bound persistent hook-worker root caching

## Why

The long-lived hook worker caches every request-supplied repository string and resolved root forever. Each distinct key can retain up to 16 KiB plus filesystem identity state.

## Acceptance

- The root cache has a small explicit maximum and deterministic eviction or clear-on-overflow behavior.
- Every hit still revalidates repository identity before reuse.
- Aliases, replaced roots, malformed paths, and eviction cannot return a stale resolved root.
- Allocation tests and benchmarks cover high-cardinality hostile requests and the common single-repository hit path.

## Sub-Tasks

- [x] Measure practical cache working set and hostile growth.
- [x] Add the smallest bounded cache policy.
- [x] Add replacement, alias, eviction, and concurrency regressions.
- [x] Run focused hook-worker tests and benchmarks.

## Notes

- Verified from finding 98.
- Re-resolution is bounded and existing `ResolvedRepoRoot.Revalidate` remains mandatory on hits.
- Confirmed on current source: every distinct request `repo` string is retained in an unbounded map for the worker lifetime. Cache hits revalidate only the resolved canonical directory, so a symlink or junction key rebound while its former target remains live can return the stale target.
- Generated adapters own one repository-scoped worker, so the practical steady-state working set is one canonical root. A limit of eight preserves generous multi-root and lexical-key slack while bounding retained request keys and filesystem identities.
- The cache now serializes resolution, revalidates every hit, re-resolves non-canonical aliases, clears before inserting a ninth distinct key, and never retains a failed resolution. Unix symlink and Windows junction fixtures share the same alias-rebinding regression contract.
- Verification passed: focused root-cache and hook-worker tests, the complete `internal/cli` package, `make test-fast`, allocation bounds, and a 100-iteration benchmark. On darwin/arm64 the canonical hit measured `49,515 ns/op`, `8,064 B/op`, `83 allocs/op`; hostile nine-root cycling measured `25,614 ns/op`, `4,444 B/op`, `46 allocs/op` per request while retaining at most eight entries.

## Deviations
