# TASK 306: Allow safe double dots in include patterns

## Why

Include validation rejects any pattern containing the substring `..`, including safe names such as `foo..bar.yml`. Traversal should be rejected by path-component semantics, not substring matching.

## Acceptance

- Only rooted patterns and lexical `..` path components are rejected.
- Safe repeated dots inside a segment remain accepted and compile through the existing glob validator.
- Mixed separators, escaped glob syntax, Windows roots, and normalization edge cases stay within the repository.
- Ingest, parser, and cross-platform tests pass.

## Sub-Tasks

- [x] Define include traversal semantics independent of host path quirks
- [x] Replace substring rejection with component-aware validation
- [x] Add safe-dot and escape tables
- [x] Run ingest and policy-source gates

## Notes

- Evidence: `internal/ingest/source_loader.go:421-435`.
- Contract: `/` is the portable glob separator; backslash keeps `path.Match`
  escape semantics. Boundary admission still treats either separator as a
  possible lexical traversal spelling, so rooted forms and exact `..`
  components fail before expansion.
- Verification: focused ingest/parser/compiler tests, Windows amd64 test-binary
  compilation, the complete uncached race and release-trust gate, vet,
  Staticcheck v0.8.1, and isolated self-hosting passed.

## Deviations

None.
