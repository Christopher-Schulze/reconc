# TASK 219: Report every review-relevant lockfile change

## Why

`lockdiff` reports rule changes, default mode, and aggregate source digest. It
validates the full envelope but does not explain changes to other
review-relevant top-level fields, source inventory/provenance, registry data,
or generation metadata. A digest difference can therefore make the report
non-empty without telling the reviewer what moved or which envelope contract
changed.

## Acceptance

- The diff contract explicitly classifies every validated top-level lockfile
  field as semantic, provenance, generated/non-reviewable, or unsupported.
- Review-relevant envelope and source inventory changes are represented in
  typed deterministic report fields and rendered by every CLI/report format.
- Pure rule-source moves are either reported as provenance moves or explicitly
  suppressed with enough summary information to explain the source digest.
- Nested order-insensitive normalization is field-scoped and covered by tests;
  ordered lists are never silently treated as sets.
- Golden tests cover every envelope field, source add/remove/move, provenance
  changes, nested list ordering, migration, and `IsEmpty` behavior.

## Sub-Tasks

- [x] Classify every lockfile envelope and provenance field
- [x] Extend typed diff and emptiness semantics
- [x] Render the new change categories consistently
- [x] Add exhaustive field and ordering golden tests
- [x] Run lockdiff, CLI, migration, and complete gates

## Notes

- Verified in `diffMaps`, `ruleFieldsChanged`, `canonicalValue`, and `IsEmpty`
  in `internal/lockdiff/diff.go`.
- The earlier claim that nested canonicalization reuses the outer key is false
  in the current code; this TASK does not rely on it.
- `Report.FieldClasses` classifies every current v6 envelope field and any
  unknown top-level field. Changes to envelope fields are emitted as typed
  canonical JSON before/after values with `semantic`, `provenance`,
  `generated`, or `unsupported` classes. Generated-only churn such as a
  lock-digest change caused by semantically equivalent rule reordering does
  not make `IsEmpty` false.
- `SourceChanges` reports stable-location content changes, additions,
  removals, and content-preserving source moves. `SourceOrderA/B` preserves
  ordered source inventory changes instead of treating the list as a set.
  `RuleProvenance` reports source-path/block moves separately from semantic
  `Changed` rule fields.
- CLI text and JSON expose envelope classes, source inventory/order changes,
  and rule provenance. Existing compact default-mode/source-digest summaries
  remain available in text output.
- Nested canonicalization remains field-scoped: explicitly set-like string
  lists are sorted recursively, while ordered command arguments, source
  precedence, and source inventory remain order-sensitive.
- Tests cover every envelope classification and change path, source
  add/remove/change/move/order, pure rule provenance moves, nested set-vs-
  ordered lists, v5 migration, deterministic ordering, and `IsEmpty`.
- Focused verification: `go test ./internal/lockdiff ./internal/cli -count=1`
  and `go test -race ./internal/lockdiff ./internal/cli -run 'TestDiff|TestRunDiff' -count=1` passed.
- The full `make test` gate was executed after implementation. Publication
  audit and the regenerated harness-pack check pass; the global suite remains
  blocked by the independent immutable-schema publication mismatch recorded in
  TASK 220. A concurrent audit append test also failed once under the full
  race-suite load but passed in an isolated `go test -race` reproduction; it is
  not attributed to this lockdiff change.

## Deviations

- Global release gate remains open pending TASK 220; no lockdiff behavior is
  weakened and no publication identity is rewritten to hide the mismatch.
