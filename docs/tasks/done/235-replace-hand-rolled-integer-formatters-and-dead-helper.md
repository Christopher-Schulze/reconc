# TASK 235: Replace hand-rolled integer formatters and dead helper

## Why

Parser, extractor, and adopt each carry a hand-written integer-to-decimal
formatter for diagnostics. The implementation prepends one byte per digit,
making it quadratic in digit count, and comments claim the helper avoids a
standard-library import even where `strconv` is already imported. Separately,
`sameFileIdentity` in user CLI lifecycle code has no production caller and is
exercised only by one assertion embedded in a broad helper test. Standard code
and deletion are clearer than maintaining misleading private utilities. The
verification pass also found that the parser-local `decodeYAMLMapping` wrapper
became unreachable after bounded decoding replaced its last caller.

## Acceptance

- Parser, extractor, and adopt use `strconv.Itoa` directly at every former
  production helper call site; no equivalent local formatter remains.
- Diagnostic strings, line numbers, rule indices, negative-input behavior, and
  generated YAML remain byte-identical for all reachable inputs.
- Imports and comments state actual dependencies and contain no claim that
  `strconv` is intentionally avoided.
- `sameFileIdentity` is removed after a repository-wide caller proof confirms
  it is not part of lifecycle ownership, update, uninstall, receipt, or
  publication behavior.
- The isolated dead-helper assertion is removed or retargeted to a live
  production identity boundary; no behavioral test is deleted to hide a
  failure.
- The uncalled parser-local `decodeYAMLMapping` forwarding wrapper is removed;
  bounded decoding remains the only implementation.
- Focused golden tests cover representative zero, positive, boundary, and
  diagnostic index values for parser, extractor, and adopt outputs.
- `go mod tidy -diff`, formatting, vet, Staticcheck, affected package tests,
  and portable-template tests remain green.

## Sub-Tasks

- [x] Inventory every production and test integer-format helper and caller
- [x] Replace the three verified production copies with `strconv.Itoa`
- [x] Prove `sameFileIdentity` is dead and remove its orphan assertion safely
- [x] Remove the unreachable parser-local decoding wrapper found by Staticcheck
- [x] Add or retain output-level regression coverage at real call boundaries
- [x] Run formatting, static analysis, affected tests, and portable gates

## Notes

- Session findings: `#1`, expanded `#31`, and `#32`.
- Primary code: `internal/parser/parser.go`,
  `internal/extractor/extractor.go`, `internal/adopt/adopt.go`, and
  `internal/usercli/lifecycle.go`.
- Test-only formatters and unrelated runtime helpers are not removed merely for
  having a similar name; each needs its own caller and benchmark justification.
- This is a no-behavior maintenance TASK, not a performance claim for ordinary
  small diagnostic indices.
- Repository-wide caller searches proved that `sameFileIdentity` had one test
  assertion and no production caller, while `decodeYAMLMapping` had no caller.
- Parser diagnostics now cover indices `-2`, `0`, `9`, `10`, and `100` at the
  real required-field boundary; extractor citations cover the corresponding
  one-based transitions, and adopt text covers empty, one-digit, and two-digit
  suggestion counts.
- Verified with focused and race tests for parser, extractor, adopt, and
  usercli; root and portable `go mod tidy -diff`; affected `go vet`; pinned
  Staticcheck `v0.8.0`; portable-template race tests; and harness-pack
  verification.

## Deviations

None.
