# TASK 210: Memoize package-manager ancestry detection

## Why

Assurance package-script checks walk from each manifest toward the repository
root and probe the same lockfile names in shared ancestor directories. Monorepos
with many manifests repeat identical directory and lockfile observations within
one evaluation.

## Acceptance

- One evaluation-scoped directory cache records stable package-manager evidence
  and parent relationships for each inspected directory.
- Shared ancestors and lockfile candidates are inspected once while each
  manifest retains its own nearest-manager and ambiguity result.
- Directory/file identity changes, symlinks, unreadable entries, and partial
  errors are not hidden by the cache.
- Manager precedence and ambiguity diagnostics remain deterministic for nested
  and mixed-manager workspaces.
- Tests cover sibling manifests, nested managers, lockfile changes, errors, and
  large monorepos; benchmarks count filesystem probes.

## Sub-Tasks

- [x] Define ancestry observation and cache identity
- [x] Implement bounded per-evaluation directory memoization
- [x] Preserve nearest-manager and ambiguity semantics
- [x] Add monorepo, mutation, and benchmark tests
- [ ] Run assurance, race, and complete gates

## Notes

- Package-script evaluation now keeps bounded directory and ancestry caches in
  the evaluation state. Each cached directory stores its normalized metadata,
  manager signals, and stable `Lstat` identity; changed directory identity or
  metadata causes a fresh lockfile-candidate scan, while failed probes are not
  cached. Ancestry entries retain the ordered nearest-manager chain and are
  invalidated when any observed directory's signals change.
- Manager precedence, ambiguity sorting, malformed-manifest handling, symlink
  filtering, and per-manifest observations remain unchanged. Cache maps are
  capped by the existing scanned-file ceiling (with a bounded ancestor headroom).
- Tests cover sibling reuse, nested nearest-manager selection, lockfile removal
  invalidation, missing-directory errors, and a benchmark reporting directory
  and lockfile probe counts. Full race and complete gates remain for queue
  completion.

## Deviations

None.
