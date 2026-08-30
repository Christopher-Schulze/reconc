# TASK 460: Render TUI width in terminal cells

## Why

`RenderTextWidth` promises to bound every line by terminal cells but truncates by rune count. Wide CJK and emoji runes can therefore exceed the requested width, while combining sequences can be split into visually broken output.

## Acceptance

- Every rendered line fits the requested terminal-cell width for ASCII, wide runes, combining marks, variation selectors, and supported emoji sequences.
- Truncation never emits invalid UTF-8 or a dangling combining/variation sequence.
- The ellipsis consumes its measured display width and widths from one through three remain deterministic.
- The implementation reuses an existing dependency or a small locally tested width contract; no terminal UI framework is introduced.

## Sub-Tasks

- [x] Define the supported Unicode display-width contract and inspect existing dependency support before adding code or dependencies.
- [x] Replace rune-count truncation with deterministic cell-width truncation.
- [x] Add table-driven ASCII, CJK, combining-mark, emoji, and narrow-width regressions.
- [x] Run focused TUI and CLI rendering tests.

## Notes

- Verified from worker finding 765 against `internal/tui/tui.go`.
- The current implementation converts each line to `[]rune` and compares `len(runes)` directly with a width documented as terminal cells.
- `golang.org/x/text/width` is already a direct dependency and supplies Unicode East Asian width properties, but not display-length or grapheme APIs. The local contract therefore treats wide/fullwidth runes as two cells, printable narrow/ambiguous runes as one, marks and join controls as zero, and keeps combining, variation-selector, emoji-modifier, keycap, regional-indicator-pair, emoji-tag, and ZWJ sequences atomic. VS15 selects one cell and VS16/emoji sequences select two.
- Focused verification passed: `go test ./internal/tui` and `go test ./internal/cli -run 'TestRunTUI(DiscoveredRepo|JSONOutput)$' -count=1`.
- Final queue integration reconciled stale generated hook artifacts, strict executable-trust setup for the isolated offline hook verifier, post-hardening fixture expectations, architecture inventory, harness-pack metadata, and publication-audit fixtures exposed by the completed TASK 374-460 changes.
- Final verification passed: affected-package tests, `make test-fast`, `make vet`, and `make lint`. The complete `make test` gate, including publication audit, race suites, and release trust, passed immediately before the final dead-wrapper and diagnostic-style cleanup; per the operator's race/release-on-request policy it was not repeated afterward.

## Deviations
