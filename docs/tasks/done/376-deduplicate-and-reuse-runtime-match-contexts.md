# TASK 376: Deduplicate and reuse runtime match contexts

## Why

Duplicate normalized write paths are evaluated repeatedly against every pattern. The memo key re-hashes the full write list per rule, and a cache miss clones the resulting contexts both into storage and again for the caller.

## Acceptance

- Duplicate write paths cannot create duplicate matching work, contexts, triggered paths, or violations.
- One evaluation computes a reusable write-set identity instead of hashing it per rule.
- Memo ownership avoids redundant deep clones while callers cannot mutate cached contexts.
- Allocation and scaling benchmarks cover duplicate-heavy writes, memo hits, and misses.

## Sub-Tasks

- [x] Measure duplicate-path and memo key/clone baselines.
- [x] Deduplicate normalized writes and introduce one evaluation-scoped identity.
- [x] Tighten immutable memo ownership and add mutation-resistance tests.
- [x] Run focused runtime tests and benchmarks.

## Notes

- Verified from findings 3, 4, and 5.
- `collectMatchContextsWithMatchers` currently iterates every normalized write; `digestStrings` is called from each memo lookup; miss handling stores and returns separate clones.
- Output ordering and template-capture conflict behavior must remain unchanged.
- Baseline (three runs): memo hits 434-1,367 ns, 816 B and 7 allocs/op; 32-context misses 32,075-35,340 ns, about 42.2 KiB and 333 allocs/op; 1,024 duplicate writes produced 1,024 contexts at 1.01-1.57 ms, about 1.29 MiB and 10,262 allocs/op.
- Optimized result (three runs): memo hits 347-353 ns, 784 B and 6 allocs/op; misses 25,710-25,729 ns, about 29.7 KiB and 268 allocs/op; duplicate writes produce one context at 9.07-9.13 microseconds, about 21.2 KiB and 16 allocs/op.

## Deviations
