# TASK 191: Bound policy glob expansion before materialization

## Why

Discovery and include handling call filesystem glob functions that materialize
the complete match slice before Reconc applies source-count limits. Broad but
syntactically accepted include patterns can therefore consume unbounded memory
and directory traversal work before the bundle's normal caps take effect.

## Acceptance

- Pattern count, pattern length, allowed scope, and total matched entries are
  bounded before or during enumeration, not after an unbounded match slice is
  allocated.
- Default `policies/*.yml` discovery uses bounded directory enumeration with
  deterministic ordering and explicit regular-file/identity checks.
- User includes have a documented scope and grammar; unsupported broad patterns
  fail with a precise source/config location.
- Duplicate matches are deduplicated without hiding identity conflicts.
- Stress tests cover huge directories, many patterns, broad matches, symlinks,
  and cap boundaries with bounded memory.

## Sub-Tasks

- [x] Define policy glob scope and cardinality limits
- [x] Implement bounded default-fragment enumeration
- [x] Implement bounded include expansion and deduplication
- [x] Add stress, identity, and boundary tests
- [x] Run ingest and complete Go gates

## Notes

- `boundedPolicyGlob` performs segment-based directory traversal with 256
  pattern/1 KiB pattern limits, bounded directory entries/directories, and a
  4,096-source match cap. It rejects empty/dot path segments and treats `**`
  as ordinary non-recursive glob text, matching Go's actual semantics.
- Discovery and source loading use the bounded enumerator; default fragment
  inventories and custom includes are deduplicated by normalized relative path
  before any body is retained. Oversized directories/patterns fail with a
  source-located error rather than allocating an unbounded match slice.
- Stress, grammar, symlink, and boundary tests pass. Benchmarking shows the
  bounded traversal is intentionally more expensive than `filepath.Glob` on a
  small directory, but its allocation is capped and security checks happen
  during enumeration rather than after materialization.

## Deviations

None.
