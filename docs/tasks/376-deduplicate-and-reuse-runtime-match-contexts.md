# TASK 376: Deduplicate and reuse runtime match contexts

## Why

Duplicate normalized write paths are evaluated repeatedly against every pattern. The memo key re-hashes the full write list per rule, and a cache miss clones the resulting contexts both into storage and again for the caller.

## Acceptance

- Duplicate write paths cannot create duplicate matching work, contexts, triggered paths, or violations.
- One evaluation computes a reusable write-set identity instead of hashing it per rule.
- Memo ownership avoids redundant deep clones while callers cannot mutate cached contexts.
- Allocation and scaling benchmarks cover duplicate-heavy writes, memo hits, and misses.

## Sub-Tasks

- [ ] Measure duplicate-path and memo key/clone baselines.
- [ ] Deduplicate normalized writes and introduce one evaluation-scoped identity.
- [ ] Tighten immutable memo ownership and add mutation-resistance tests.
- [ ] Run focused runtime tests and benchmarks.

## Notes

- Verified from findings 3, 4, and 5.
- `collectMatchContextsWithMatchers` currently iterates every normalized write; `digestStrings` is called from each memo lookup; miss handling stores and returns separate clones.
- Output ordering and template-capture conflict behavior must remain unchanged.

## Deviations
