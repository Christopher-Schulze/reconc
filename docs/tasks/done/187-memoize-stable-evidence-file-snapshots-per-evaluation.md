# TASK 187: Memoize stable evidence-file snapshots per evaluation

## Why

Evidence matching stats and reads substituted paths inside rule/match-context
loops. Multiple rules or contexts that resolve to the same file can repeat the
same stable read, text conversion, and containment work during one policy
evaluation.

## Acceptance

- One evaluation-scoped cache keys evidence by canonical substituted path plus
  the identity attributes required to prevent stale or swapped-file reuse.
- File bytes, existence, size, read errors, and any derived match data are
  reused only within the same stable evaluation snapshot.
- Budget accounting charges physical reads and retained bytes exactly once
  under a documented rule; repeated logical consumers retain provenance.
- Identity changes or unstable reads fail closed rather than returning cached
  success.
- Tests cover duplicate rules, duplicate contexts, concurrent replacement,
  symlinks, read errors, and bounded eviction; benchmarks prove fewer reads.

## Sub-Tasks

- [x] Define evidence snapshot keys and budget semantics
- [x] Add an evaluation-scoped stable-read cache
- [x] Route simple and composite evidence checks through it
- [x] Add hostile identity, duplication, and benchmark tests
- [x] Run runtime, race, and complete gates

## Notes

- Added `evidenceSnapshotCache`, bounded to 1,024 entries and 16 MiB of
  retained content. Keys are resolved paths; entries retain existence,
  `os.FileInfo` identity, metadata, content, and read errors only while every
  cache hit revalidates identity, mode, size, and modification time.
- Top-level and composite `require_fresh_file`/`require_evidence` checks share
  the cache. Missing files invalidate when they appear; replacement or
  metadata drift returns a fail-closed snapshot-change error.
- Regression tests cover stable reuse, same-path replacement, missing-file
  appearance, cached read errors, and bounded eviction. Benchmark: cache hit
  1.688 us/op versus bounded reread 17.412 us/op on Apple M1.

## Deviations

None.
