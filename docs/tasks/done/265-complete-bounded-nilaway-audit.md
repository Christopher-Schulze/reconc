# TASK 265: Complete the bounded NilAway audit

## Why

The release general rehearsal's exploratory NilAway invocation analyzed
dependencies without a first-party package filter and was stopped before it
produced a result. The supported package-prefix configuration must be used to
obtain complete, actionable nil-flow evidence without wasting local resources.

## Acceptance

- The current NilAway release analyzes every first-party package in the root
  and portable-template Go modules with explicit package-prefix filters, both
  with production-only diagnostics and with test diagnostics enabled.
- Every diagnostic is reproduced in its owning package and classified against
  the actual source contract; any validated nil-flow defect is fixed with a
  focused regression test.
- Relevant formatting, focused tests, Vet, Staticcheck, and bounded NilAway
  analysis pass on the final tree without adding NilAway as a shipped
  dependency or preserving a false-positive baseline.
- TASK state and the final diff are archived in one local main commit; no push,
  tag, release, or branch is created.

## Sub-Tasks

- [x] Run bounded first-party NilAway analysis for both modules
- [x] Reproduce and fix every validated diagnostic
- [x] Run focused and final verification
- [x] Archive TASK 265 and commit locally on main

## Notes

- NilAway's documentation explicitly recommends `include-pkgs` because the
  standalone driver otherwise analyzes the standard library and third-party
  dependencies in memory. The previous unfiltered invocation therefore did not
  measure Reconc efficiently.
- The audited release was
  `v0.0.0-20260808063849-8649a03c818a` from 2026-08-08. The bounded root scan
  completed with 130 unique flows when test diagnostics were enabled. Of
  those, 67 point directly into test files; the production-only report
  contains 54 unique flows. The portable-template scan contains 18 unique
  production flows and no additional test-only flow.
- Every one of the 72 production flows was checked at its producer and
  consumer. They divide into four analyzer limitations: 12 inline Go
  short-circuit guards, 37 successful-error/result correlations, 6 nil-safe
  slice/map or dominating-branch cases, and 17 internal constructor/invariant
  correlations. The additional root test flows are the same families plus
  `testing.T.Fatal` paths that NilAway does not treat as terminating. No flow
  is reachable as a nil dereference under its actual contract, so no product
  or test code change was justified.
- Representative contracts were checked directly: action-state validation
  binds every reservation charge to an existing budget before charge helpers
  run; transactional JSONL appends allocate a journal whenever `commit` is
  non-nil; bootstrap and user-CLI operations construct non-nil reports on
  successful paths; filesystem metadata is consumed only after its associated
  error has been rejected; nil slices are only ranged, measured, or sliced at
  proven bounds.
- Final verification passed: bounded production and test-inclusive NilAway
  runs for both modules, `make fmt-check vet lint`, focused root tests for
  action state, JSONL, parser, agent-session runtime, and task lifecycle, plus
  all portable-template audit and utility tests.

## Deviations

None.
