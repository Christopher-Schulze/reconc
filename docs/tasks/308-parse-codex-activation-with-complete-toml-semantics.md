# TASK 308: Parse Codex activation with complete TOML semantics

## Why

The Codex activation reader strips comments one line at a time and cannot carry quote state across multiline basic or literal strings. Text inside such strings can be misclassified as comments or as `features.hooks` configuration.

## Acceptance

- Activation inspection follows complete TOML string, comment, table, dotted-key, and duplicate-key semantics.
- Multiline strings containing `#`, section-like text, or `features.hooks` never affect activation state.
- User formatting and unrelated content remain byte-preserved when the managed value is updated.
- TOML fixtures, hook status/install, and compatibility tests pass.

## Sub-Tasks

- [ ] Specify the parsed activation ownership boundary
- [ ] Reuse the existing TOML dependency without a parallel partial parser
- [ ] Preserve surgical rendering of the managed value
- [ ] Add multiline, duplicate, and formatting regressions

## Notes

- Evidence: `internal/hooks/codex_activation.go:180-235`.

## Deviations

None.
