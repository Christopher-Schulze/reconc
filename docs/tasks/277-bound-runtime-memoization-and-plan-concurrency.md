# TASK 277: Bound runtime memoization and plan concurrency

## Why

Runtime evaluation still has an entry-count-only match-context memo that can
retain large cloned capture maps, hashes evidence options for every assertion,
allocates empty capture maps for literal templates, and performs matcher lookup
inside the innermost forbidden-command loop. The evaluator also holds one mutex
across lockfile reads, policy-source I/O, freshness walks, compilation, and
cache publication, serializing otherwise independent roots and concurrent
worker requests. Composite checks ignore their configured script kill timeout.

## Acceptance

- Match-context memoization has an exact byte budget covering keys, paths,
  captures, errors, slices, and cloned return values. Oversized entries are not
  retained and deterministic FIFO/eviction semantics remain documented.
- Evidence option keys avoid per-call hash construction where a comparable
  bounded representation is possible; slice content remains part of equality
  and no mutable input aliases a cache key.
- Literal template matches return `nil` captures and allocate no map. Consumers
  remain nil-safe and variable templates preserve exact binding behavior.
- Forbidden-command rules hoist precompiled expected matchers outside observed
  command/segment loops. Exact/prefix/case-folding and uncertainty semantics are
  unchanged.
- Runtime plan loading uses per-root singleflight or equivalent narrow locking.
  Different roots and cache hits do not wait behind unrelated filesystem I/O;
  concurrent loads of one root publish one validated plan.
- Any load performed outside the cache mutex revalidates lockfile/source
  identity at publication. No stale plan can win a race with refresh or source
  mutation.
- Composite `require_script` checks honor their declared `kill_timeout_sec`
  within existing global bounds, matching top-level script behavior.
- Duplicate summary rendering is consolidated to one canonical owner and
  output remains byte-stable.
- Allocation, concurrency, mutation-race, timeout, fuzz, benchmark-history,
  docs, and complete gates pass.

## Sub-Tasks

- [~] Measure memo entry sizes, matcher allocations, and plan-lock contention
- [ ] Add byte accounting and bounded eviction to match-context memoization
- [ ] Replace evidence-option hash keys only with an exact immutable comparable key
- [ ] Remove empty literal-template capture allocations
- [ ] Hoist expected command matchers per rule
- [ ] Replace global plan-load locking with per-root singleflight and publication revalidation
- [ ] Honor composite script kill timeouts and consolidate summary ownership
- [ ] Add allocation, contention, mutation, timeout, and race tests
- [ ] Update runtime cache/concurrency documentation and benchmark history
- [ ] Run runtime, fuzz, race, publication, and complete repository gates

## Notes

- Current evidence: `matchContextMemo` is capped at 4096 entries but has no byte
  counter; entries clone `[]matchContext` and capture maps on store and return.
- Current evidence: `digestEvidenceOptions` constructs SHA-256 state per
  assertion; `compiledTemplateMatcher.match` returns an allocated empty map for
  literal patterns.
- Current evidence: `loadRuntimePlan` holds `e.mu` from `readLockfileBytes`
  through source loading, freshness, decode, compile, and cache publication.
- Re-statting evidence snapshots on every hit and hashing freshness files on
  every plan validation are intentional mutation-detection guarantees. Do not
  skip them based only on inode, size, and mtime; metadata can be restored while
  bytes change.
- A content-read error poisoning later metadata-only reads is fail-closed. Keep
  that behavior unless a separate contract task proves that partial evaluation
  remains safe.

## Deviations

None.
