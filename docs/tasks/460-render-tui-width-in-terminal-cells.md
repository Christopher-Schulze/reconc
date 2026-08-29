# TASK 460: Render TUI width in terminal cells

## Why

`RenderTextWidth` promises to bound every line by terminal cells but truncates by rune count. Wide CJK and emoji runes can therefore exceed the requested width, while combining sequences can be split into visually broken output.

## Acceptance

- Every rendered line fits the requested terminal-cell width for ASCII, wide runes, combining marks, variation selectors, and supported emoji sequences.
- Truncation never emits invalid UTF-8 or a dangling combining/variation sequence.
- The ellipsis consumes its measured display width and widths from one through three remain deterministic.
- The implementation reuses an existing dependency or a small locally tested width contract; no terminal UI framework is introduced.

## Sub-Tasks

- [ ] Define the supported Unicode display-width contract and inspect existing dependency support before adding code or dependencies.
- [ ] Replace rune-count truncation with deterministic cell-width truncation.
- [ ] Add table-driven ASCII, CJK, combining-mark, emoji, and narrow-width regressions.
- [ ] Run focused TUI and CLI rendering tests.

## Notes

- Verified from worker finding 765 against `internal/tui/tui.go`.
- The current implementation converts each line to `[]rune` and compares `len(runes)` directly with a width documented as terminal cells.

## Deviations
