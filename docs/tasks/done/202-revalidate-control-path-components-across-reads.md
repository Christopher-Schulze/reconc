# TASK 202: Revalidate TASK path components across reads

## Why

TASK run-state inspection uses a path guard that checks fewer file types than
the configuration guard, caches previously seen components, and does not prove
those component identities again after overview and detail reads. Directory
replacement between phases can redirect a detail read even when overview bytes
remain unchanged.

## Acceptance

- One canonical TASK filesystem guard rejects symlinks, reparse points,
  irregular files, non-directory intermediates, and identity changes for both
  config and run-state paths.
- Overview and detail reads are bound to one stable directory/component
  snapshot and revalidated before a result is accepted.
- Guard caching stores verified identities, not only path strings, and never
  skips a required post-read check.
- Tests deterministically swap task directories/files between every phase and
  prove fail-closed behavior on Unix and Windows.
- Existing TASK validation diagnostics and read-only behavior remain intact.

## Sub-Tasks

- [x] Consolidate TASK filesystem invariants in one helper
- [x] Bind overview and detail reads to a stable snapshot
- [x] Replace path-string guard caching with identity-aware state
- [x] Add swap, irregular-file, and platform tests
- [x] Run task lifecycle, race, and complete gates

## Notes

- Verified divergence between `internal/tasklifecycle/run_state.go` and
  `internal/tasklifecycle/config.go`.

## Deviations

None.
