# TASK 277: Bound runtime memoization and plan concurrency

## Why

Runtime evaluation still has an entry-count-only match-context memo that can
retain large cloned capture maps, hashes evidence options for every assertion,
allocates empty capture maps for literal templates, and performs matcher lookup
inside the innermost forbidden-command loop. The evaluator also holds one mutex
across lockfile reads, policy-source I/O, freshness walks, compilation, and
cache publication, serializing otherwise independent roots and concurrent
worker requests. The audit also claimed composite checks ignored a configured
script kill timeout, but the stable composite-check contract exposes no such
field and already routes zero through the bounded default.

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
- Composite `require_script` checks keep the documented bounded default kill
  grace. The nonexistent sub-check field remains rejected and no lockfile or
  schema identity is changed to manufacture a new contract.
- Duplicate summary rendering is consolidated to one canonical owner and
  output remains byte-stable.
- Allocation, concurrency, mutation-race, timeout, fuzz, benchmark-history,
  docs, and complete gates pass.

## Sub-Tasks

- [x] Measure memo entry sizes, matcher allocations, and plan-lock contention
- [x] Add byte accounting and bounded eviction to match-context memoization
- [x] Replace evidence-option hash keys only with an exact immutable comparable key
- [x] Remove empty literal-template capture allocations
- [x] Hoist expected command matchers per rule
- [x] Replace global plan-load locking with per-root singleflight and publication revalidation
- [x] Preserve the composite script kill-timeout contract and consolidate summary ownership
- [x] Add allocation, contention, mutation, timeout, and race tests
- [x] Update runtime cache/concurrency documentation and benchmark history
- [x] Run runtime, fuzz, race, publication, and complete repository gates

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
- Composite `require_script` checks have never declared `kill_timeout_sec` in
  the authoring schema, policy type, field matrix, or lock schema. Passing zero
  to `RunScript` selects the tested five-second bounded default. Adding the
  field would alter a stable format-6 contract, so the incorrect audit premise
  was rejected rather than implemented as undocumented format churn.
- Fuzzing found an upstream lazy-validation edge case where
  `doublestar.Match("[!00", "0")` returned a miss without reporting the
  malformed character class. `MatchPath` now validates the complete pattern
  before the unchecked match, restoring exact parity with compiled matchers.
- Verification: parser/runtime race suites passed; the two matcher fuzz targets
  completed 222,720 generated executions after the regression repair; benchmark
  history v9 recorded and compared cleanly; `test-fast` passed every package
  before its final test-log write exhausted disk, and the affected
  `runtime/agentsession` package then passed independently after clearing only
  Go build/test caches. Vet, Staticcheck, reference-doc checks,
  publication-audit, and harness-pack verification passed.

## Deviations

None.
