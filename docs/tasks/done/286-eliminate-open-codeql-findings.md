# TASK 286: Eliminate open CodeQL findings

## Why

GitHub code scanning reports 19 open CodeQL alerts on `main`: 18 critical
`go/unsafe-quoting` findings in runtime diagnostic construction and one high
`go/allocation-size-overflow` finding in evidence-result capacity arithmetic.
The quoting flows do not reach command, SQL, or code execution, but untrusted
policy values can still break the diagnostic delimiters. The allocation input
is contract-bounded, but the local addition should remain intrinsically safe.

## Acceptance

- Every dynamic rule identity and evidence assertion is escaped through one
  canonical diagnostic-quoting boundary.
- Runtime diagnostics preserve their meaning while safely representing single
  quotes, double quotes, backslashes, control characters, and Unicode.
- Evidence-result construction contains no overflowing capacity arithmetic.
- Focused unit, full runtime race, formatting, vet, and static analysis gates
  pass.
- Fresh CodeQL on the exact pushed commit succeeds and GitHub reports zero open
  code-scanning alerts on `main`.

## Sub-Tasks

- [x] Replace unsafe diagnostic quoting at all 18 reported flows
- [x] Remove the overflowing allocation-capacity expression
- [x] Add adversarial diagnostic regression tests
- [x] Run focused and package-level verification
- [x] Commit and push the verified fix to `main`
- [x] Require fresh green CI and CodeQL and verify zero open alerts
- [x] Archive the completed TASK

## Notes

- Alerts `42` through `59` are `go/unsafe-quoting`; alert `40` is
  `go/allocation-size-overflow`.
- All alerts currently point to release commit
  `3119b689aca36792b9fb69c8ec1965169114f669`.
- The immutable `reconc-v0.9.7` tag is not moved or replaced. This correction
  applies to post-release `main` and requires a later release to ship as a
  binary update.
- Focused regression coverage proves delimiter and control-character escaping
  for plain text, single quotes, double quotes, newlines, and Unicode.
- Verification passed for focused regressions, the complete runtime package,
  the complete runtime race suite, formatting, Vet, and Staticcheck.
- Candidate `2305940fafafcf110a74b67e83961c15c95af2b6` passed CI run
  `32633852831`, including the complete native Windows suite, and CodeQL run
  `32633852809`.
- GitHub's code-scanning API returned an empty open-alert array and
  `OPEN_COUNT=0`; no alert was dismissed.

## Deviations
