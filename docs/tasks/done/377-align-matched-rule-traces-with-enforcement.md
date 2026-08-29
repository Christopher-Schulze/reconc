# TASK 377: Align matched-rule traces with enforcement

## Why

`matchedRuleIDs` evaluates composite rules without the current command list. Its trigger path can therefore report a `forbid_command` composite as matched from paths alone even when enforcement correctly determines that no forbidden command occurred.

## Acceptance

- `EvaluationTrace.MatchedRuleIDs` contains exactly the rules whose complete trigger predicates fired.
- Composite path-and-command rules use the same normalized command evidence for tracing and enforcement.
- Trace computation does not execute scripts or duplicate enforcement side effects.
- Table-driven regressions cover path-only, command-only, composite hit, and composite miss cases.

## Sub-Tasks

- [x] Define one side-effect-free trigger contract shared by metrics and enforcement.
- [x] Thread current commands into matched-rule computation.
- [x] Add parity regressions across filtered and full checks.
- [x] Run focused runtime trace tests.

## Notes

- Verified from finding 7.
- `matchedRuleIDs` builds a context with nil `currentCommands`; `ruleTriggerMatches` treats the composite path portion as sufficient in that path.
- Added one side-effect-free composite trigger predicate shared by trace accounting and pre-command enforcement. It checks the parent path and current-command trigger without evaluating sub-checks.
- Regression coverage proves path-only, command-only, composite hit/miss, historical-result isolation, and single execution of a `require_script` side effect.
- Verified with focused runtime trace tests, uncached runtime and Impact Lab package tests, `make test-fast`, and `git diff --check`.

## Deviations
