# TASK 429: Parse Kimi managed blocks structurally

## Why

Kimi hook ownership locates managed boundaries with raw substring counts and indexes. Marker text inside TOML comments or string values can therefore be mistaken for structural ownership and remove unrelated bytes.

## Acceptance

- Managed Kimi blocks are recognized only at valid structural boundaries owned by Reconc.
- Marker-like text in comments, strings, arrays, and unrelated tables is preserved byte-for-byte.
- Ambiguous, nested, duplicated, or malformed boundaries fail closed without mutation.
- Adversarial round-trip tests cover every marker placement and mixed user content.

## Sub-Tasks

- [ ] Specify the exact Kimi managed-block grammar and ownership boundary.
- [ ] Replace raw substring slicing with a bounded structural scanner.
- [ ] Add preservation and malformed-boundary regressions.
- [ ] Run focused Kimi install/remove tests.

## Notes

- Verified from finding 108 in `internal/hooks/kimi.go`.

## Deviations
