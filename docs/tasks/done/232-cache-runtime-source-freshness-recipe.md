# TASK 232: Cache the runtime source-freshness recipe

## Why

Runtime-plan cache hits correctly perform a bounded source-freshness
observation before reusing a plan. That hit path nevertheless reads and parses
the unchanged compiler configuration on every event to recover `include`
patterns. The cached plan already represents a source bundle built from that
configuration. A plan-owned immutable freshness recipe can reuse those parsed
patterns while the separately observed configuration-file content identity
still invalidates the plan immediately when configuration changes.

## Acceptance

- Runtime-plan construction captures an immutable bounded freshness recipe
  containing the validated include patterns and any derived glob bases needed
  for a cache-hit observation.
- A stable cache hit performs no YAML decode and does not reread configuration
  solely to rediscover include patterns.
- The configuration file itself, both config candidates, discovery markers,
  source files, relevant directories, preset/global state, and custom runtimes
  remain in the observed freshness identity.
- Changing, replacing, removing, or adding compiler configuration invalidates
  the old plan before evaluation; new include patterns are loaded only through
  the existing full source-load path.
- Adding, removing, renaming, replacing, or changing a file matched by an
  unchanged include pattern invalidates the cached plan exactly as before.
- Missing, malformed, oversized, symlinked, unsupported, or identity-drifting
  configuration remains fail-closed and cannot fall back to a stale plan.
- Recipe data is defensively owned by the immutable plan, bounded by current
  parser/cardinality limits, and included in cache memory accounting where
  relevant.
- Benchmarks prove zero YAML decodes on stable hits and compare cold load,
  stable hit, config edit, include-set edit, and large included source sets.
- Existing freshness mutation, LRU, race, hook-worker, and Windows identity
  tests remain green.

## Sub-Tasks

- [x] Define the immutable source-freshness recipe and ownership contract
- [x] Capture validated include data during the full source-load path
- [x] Use the recipe on cache hits without weakening observed identities
- [x] Add decode-count and complete invalidation-matrix tests
- [x] Benchmark stable and invalidated hits and run runtime/hook-worker gates

## Notes

- Session finding: `#33`.
- Primary code: `internal/runtime/runtime_plan.go`,
  `internal/runtime/source_freshness.go`, and the ingest configuration model in
  `internal/ingest/source_loader.go`.
- TASK 182 intentionally introduced complete freshness observation before
  cache reuse. This TASK optimizes that observer; it must not replace it with
  mtime-only checks or trust cached configuration after a content change.
- A config content mismatch is sufficient to reject the old recipe before the
  newly declared include set matters, which is the key safety invariant.
- `SourceBundle` now privately owns the validated, sorted, unique include
  patterns used for its policy-file tier and returns only defensive copies.
  The runtime plan converts them once into a root-bound recipe with precomputed
  glob bases, a 256-pattern/1-KiB-per-pattern/256-KiB aggregate ceiling, and no
  caller-owned slice storage.
- Cache-hit observation no longer imports or invokes a YAML decoder. It still
  reads the config as a bounded identity-checked freshness file, plus both
  candidates, discovery markers, policy/global/preset/custom-runtime sources,
  and relevant directories. Include-set observation reuses ingest's bounded
  segment glob rather than `filepath.Glob`. A malformed post-cache config
  changes the freshness hash without being decoded by the observer, then fails
  closed when the authoritative full loader decodes it.
- Tests cover stable equality; config edit, same-content replacement, removal,
  primary/candidate addition, malformed YAML, unsupported include, oversize,
  and symlink; included-source addition, removal, rename, same-content
  replacement, and same-size content drift; defensive recipe ownership and
  bounds; LRU and worker reuse remain covered.
- Apple M1 benchmark medians over three 200-ms samples were approximately
  1.54 ms/195 KB/476 allocations for a configured stable hit, 3.27 ms/220
  KB/1,469 allocations for a cold load, 1.61 ms/180 KB/365 allocations for a
  config edit observation, 1.74 ms/200 KB/386 allocations for an include-set
  edit observation, and 20.4 ms/5.39 MB/5,223 allocations for 128 sources.
  These are local scenario measurements, not global performance claims.
- Verification: full ingest and runtime tests, focused persistent-worker
  tests, focused ingest/runtime race tests, and `go vet` across ingest, runtime,
  and agent-session runtime passed.

## Deviations

None.
