# TASK 366: Parse assurance guard markers outside comments and strings

## Why

Assurance marker detection searches raw nearby lines and only skips whole comment lines. An inline comment or string literal can satisfy a required hardening marker without a real guard call.

## Acceptance

- Guard markers count only as executable syntax in the required nearby scope.
- Line comments, block comments, raw strings, and interpreted strings cannot satisfy a marker.
- Legitimate qualified calls and supported syntax variants remain accepted.
- Tests cover inline comments, multiline comments, strings, and real calls.

## Sub-Tasks

- [x] Replace raw substring proximity checks with syntax-aware marker detection.
- [x] Preserve configured proximity and supported call forms.
- [x] Add false-positive and true-positive regressions.
- [x] Run focused assurance tests.

## Notes

- Verified from OMP session `01a04cc5-6a01-7526-95bb-b2715553daf3`, finding #112.
- Current evidence: `internal/assurance/gates.go` applies `strings.Contains` to raw non-whole-comment lines.
- TASK 304 fixed comment-line classification but not inline comments or strings.
- Guard scans now make one forward lexical pass per file, masking line/block comments plus interpreted, raw, and multiline strings before matching sites or markers. Qualified executable marker calls remain visible.
- Regressions cover inline and multiline comments, interpreted and multiline raw strings, string-only sites, and a qualified executable guard. `go test ./internal/assurance -count=1 -timeout=120s`, `go vet ./internal/assurance`, `make fmt-check`, and `make reference-docs-check` passed.

## Deviations

- The repository-wide race, release-trust, publication, and other heavy suites were not run, per the explicit execution constraint. Windows-specific tests were not run locally; cross-platform source compatibility remains covered by CI.
