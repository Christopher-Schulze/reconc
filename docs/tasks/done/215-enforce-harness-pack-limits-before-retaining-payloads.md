# TASK 215: Enforce harness-pack limits before retaining payloads

## Why

Harness-pack build and archive load bound each file and the final manifest, but
they can retain many maximum-size bodies before validating the aggregate byte
limit. Archive loading also relies on positional entries and header-declared
sizes before fully proving unique canonical names and cumulative actual bytes.
The nominal 16 MiB pack cap can therefore be enforced only after much larger
memory use.

## Acceptance

- File-count, unique-name, manifest-position/identity, per-file, and aggregate
  actual-byte limits are enforced before retaining or reading beyond the
  remaining budget.
- Duplicate names, extra manifest entries, directories, irregular modes,
  misleading compressed/uncompressed sizes, truncated streams, and zip bombs
  fail deterministically.
- Build stops before reading a file that cannot fit the remaining aggregate
  budget and does not retain all bodies when metadata-only construction is
  sufficient.
- Archive order remains canonical or is validated by name through an explicit
  compatibility contract.
- Stress tests assert bounded allocations for maximum files and adversarial zip
  headers; round-trip fixtures remain compatible.

## Sub-Tasks

- [x] Define canonical archive inventory and aggregate-budget semantics
- [x] Enforce remaining-budget reads during build and load
- [x] Reject duplicate and misleading archive entries
- [x] Add zip-bomb, truncation, boundary, and allocation tests
- [x] Run harness-pack and complete gates

## Notes

- `Build` filters the canonical inventory before allocation, rejects an empty
  post-exclusion pack, checks each declared `fs.FileInfo.Size()` against both
  the per-file and remaining aggregate budgets, and reads with a remaining
  budget limiter. Actual bytes are checked again before adding manifest data.
- `Load` applies the same remaining aggregate budget to source reads. Archive
  loading validates the complete entry inventory before opening any payload:
  manifest position, unique names, canonical paths, regular-file modes, and
  file-count bounds are explicit. Each payload read is limited by both
  `MaxFileBytes` and the remaining `MaxTotalBytes`; header-declared sizes and
  actual decompressed bytes must agree, and the final actual total must equal
  the manifest total.
- Duplicate entries, directories, truncated archives, misleading size
  declarations, source drift, and remaining-budget rejection are covered by
  focused tests. The canonical generated `harness/advanced-pack-manifest.json`
  and `harness/advanced-pack.zip` were regenerated from the current template
  inventory and pass `go run ./scripts/build/harness-pack --check`.

## Deviations

None.
