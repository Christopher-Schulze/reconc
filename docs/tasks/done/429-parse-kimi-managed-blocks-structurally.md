# TASK 429: Parse Kimi managed blocks structurally

## Why

Kimi hook ownership locates managed boundaries with raw substring counts and indexes. Marker text inside TOML comments or string values can therefore be mistaken for structural ownership and remove unrelated bytes.

## Acceptance

- Managed Kimi blocks are recognized only at valid structural boundaries owned by Reconc.
- Marker-like text in comments, strings, arrays, and unrelated tables is preserved byte-for-byte.
- Ambiguous, nested, duplicated, or malformed boundaries fail closed without mutation.
- Adversarial round-trip tests cover every marker placement and mixed user content.

## Sub-Tasks

- [x] Specify the exact Kimi managed-block grammar and ownership boundary.
- [x] Replace raw substring slicing with a bounded structural scanner.
- [x] Add preservation and malformed-boundary regressions.
- [x] Run focused Kimi install/remove tests.

## Notes

- Verified from finding 108 in `internal/hooks/kimi_code.go`.
- Exact standalone column-one TOML comment expressions are the only ownership
  boundaries. The existing 4 MiB managed-artifact read limit bounds parsing.
- Replacement uses the parser-derived byte range, never a second substring
  search. Marker-like values, nested comments, trailing comments, indentation,
  multiline strings, mixed line endings, and exact embedded generated content
  round-trip byte-for-byte.
- Duplicate, nested, reversed, and unpaired structural markers fail before
  publication. Focused Kimi tests and reference-doc checks passed.

## Deviations
