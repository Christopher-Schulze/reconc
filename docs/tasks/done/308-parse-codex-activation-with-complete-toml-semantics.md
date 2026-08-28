# TASK 308: Parse Codex activation with complete TOML semantics

## Why

The Codex activation reader strips comments one line at a time and cannot carry quote state across multiline basic or literal strings. Text inside such strings can be misclassified as comments or as `features.hooks` configuration.

## Acceptance

- Activation inspection follows complete TOML string, comment, table, dotted-key, and duplicate-key semantics.
- Multiline strings containing `#`, section-like text, or `features.hooks` never affect activation state.
- User formatting and unrelated content remain byte-preserved when the managed value is updated.
- TOML fixtures, hook status/install, and compatibility tests pass.

## Sub-Tasks

- [x] Specify the parsed activation ownership boundary
- [x] Reuse the existing TOML dependency without a parallel partial parser
- [x] Preserve surgical rendering of the managed value
- [x] Add multiline, duplicate, and formatting regressions

## Notes

- Evidence: `internal/hooks/codex_activation.go:180-235`.
- Contract: `go-toml/v2` is authoritative for semantic decoding and duplicate
  rejection. Its syntax tree supplies source ranges only, so updates replace
  the parsed boolean or extend the parsed inline table without reformatting
  unrelated bytes. Marker discovery likewise accepts only real TOML comments.
- Verification: focused activation and status tests, the complete hooks race
  suite, Windows amd64 test-binary compilation, the complete uncached race and
  release-trust gate, vet, Staticcheck v0.8.1, and isolated self-hosting passed.

## Deviations

None.
