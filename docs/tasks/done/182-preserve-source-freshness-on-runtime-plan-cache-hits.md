# TASK 182: Preserve source freshness on runtime-plan cache hits

## Why

`loadRuntimePlan` reads and parses the lockfile, reloads all policy sources,
and computes their digest before it checks the runtime-plan cache. Hook workers
retain an evaluator across events, so nominal cache hits still pay source
discovery, bounded reads, parsing support work, hashing, and filesystem identity
resolution on every request.

## Acceptance

- Runtime-plan cache hits avoid full source loading when a bounded freshness
  identity proves the source set unchanged.
- Freshness covers additions, removals, renames, config include/extends changes,
  inline blocks, symlink/identity changes, and content changes without relying
  only on coarse mtimes.
- Any uncertain, oversized, unsupported, or failed freshness observation falls
  back safely or fails closed; stale policy is never evaluated.
- Benchmarks measure cold load, stable worker hit, single-source edit, and large
  source sets and establish a material hit-path improvement.
- Runtime cache bounds, eviction, concurrency, and existing freshness tests pass.

## Sub-Tasks

- [x] Define a complete bounded policy-source freshness identity
- [x] Add the cache lookup path before full source loading
- [x] Preserve fail-closed fallback and invalidation semantics
- [x] Add mutation, identity, concurrency, and benchmark coverage
- [x] Run runtime, hook-worker, race, and complete gates

## Notes

- Verified in `internal/runtime/runtime_plan.go:91-139` and
  `internal/cli/hook_worker.go`.
- `internal/runtime/source_freshness.go` observes a bounded canonical identity
  over discovery markers, configured glob directories, selected source
  descriptors, exact content digests, file identities, and relevant directory
  entries. It detects additions, removals, renames, inline/config changes,
  custom runtimes, global policy changes, preset overrides, symlinks, and
  identity swaps without reparsing the full source bundle.
- Stable worker hits perform the bounded freshness observation before
  `ingest.LoadPolicySources`; uncertain or failed observations discard the
  cache and take the existing full-load/fail-closed path. Runtime plan caches
  are capped at 32 entries with mutex-protected least-recently-used eviction.
- Added mutation, configured-include, same-size content, symlink, LRU, and
  benchmark coverage. Benchmarks cover cold load, stable hit, single-source
  edits, and a 128-source set. `make test` passed with race-enabled runtime,
  hook-worker, harness, release-trust, and publication gates; the runtime test
  package also cross-compiled for Windows.

## Deviations

None.
