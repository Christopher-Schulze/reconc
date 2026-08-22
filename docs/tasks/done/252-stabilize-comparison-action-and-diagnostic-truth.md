# TASK 252: Stabilize comparison, action, and diagnostic truth

## Why

Several read-only reports can currently lose the real failure cause, compare
different live inputs, or publish misleading evidence. This includes agent
guide slicing, Grok diagnostics, action ledger timestamp and budget errors,
confusable credential labels, impact comparison drift, long Git lock waits,
status severity clobbering, swallowed TUI observation errors, and over-broad
proof redaction.

## Acceptance

- Agent-guide section extraction stops at the next heading whose level is equal
  to or higher than the selected heading; top-level section listing remains
  unchanged.
- Doctor reports process execution failure before secondary output-size detail
  and retains both facts when useful.
- Action lifecycle reconstruction rejects malformed timestamps explicitly.
  Budget candidate construction returns typed errors instead of representing
  corruption as an empty candidate set.
- Sensitive-label inspection uses a bounded, documented confusable skeleton for
  the characters needed by protected vocabulary; small-capital and fullwidth
  variants cannot evade the same finding.
- Impact current and candidate evaluation bind to the same filesystem identity
  snapshot or abort with a drift error. No live change is attributed to policy.
- Command-proof index-lock retry has one bounded total deadline with typed or
  structurally recognized lock contention and capped backoff; worst-case wait
  is measured in seconds, not forty command timeouts.
- Diagnostic classification is monotonic by severity. Shadowing or ambiguity
  can add detail but cannot overwrite stale or invalid ownership state.
- TUI surfaces bounded audit and session observation errors in `View.Errors`.
- Proof redaction replaces the canonical absolute repository root but does not
  globally replace common basename tokens in commands or evidence.
- Focused unit, property, race, drift, timeout, Unicode, TUI, proof, and full
  repository tests pass; user-facing diagnostic documentation is updated.

## Sub-Tasks

- [x] Correct heading-level section boundaries and Grok diagnostic precedence
- [x] Reject malformed lifecycle timestamps and propagate budget-state errors
- [x] Define and test bounded confusable handling for sensitive vocabulary
- [x] Bind impact comparisons to one stable filesystem observation
- [x] Replace command-proof lock retry with one short bounded deadline
- [x] Make diagnostic severity monotonic and surface TUI observation failures
- [x] Restrict proof redaction to canonical path identities
- [x] Run focused, Unicode, timeout, race, proof, TUI, and full gates
- [x] Update agent-guide, impact, action, and diagnostic documentation

## Notes

- External findings: F-42, F-44, F-67, F-74, F-75, F-82, F-83, F-84,
  F-96, and F-98.
- The confusable implementation must not become an ad-hoc endless switch. Use a
  small generated or data-driven table whose protected vocabulary and Unicode
  source are testable and reviewable.
- F-99 is excluded because proof bundles load only current successful proofs;
  `fresh: true` is the exported assertion of that precondition.
- Focused tests cover malformed timestamps, typed budget corruption, Unicode
  small-capital/fullwidth forms, same-metadata content drift, typed Git lock
  contention and total deadline, monotonic installation status, real TUI audit
  and session failures, and common-basename proof text.
- The first combined race run exposed two timing-sensitive lock-test outcomes.
  The production retry now preserves a typed contention observed before the
  Git command, including when the lock disappears before post-failure
  inspection, and retains the explicit lock-deadline error when the context
  expires. The complete command-proof race package is green after correction.
- Final verification: focused package tests, the complete command-proof race
  package, `make test` (including full `go test -race -count=1 ./...`, template
  races, release-trust fixture, publication audit, and harness-pack check),
  `make vet`, and `make lint` all passed.

## Deviations

None.
