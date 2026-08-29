# TASK 377: Align matched-rule traces with enforcement

## Why

`matchedRuleIDs` evaluates composite rules without the current command list. Its trigger path can therefore report a `forbid_command` composite as matched from paths alone even when enforcement correctly determines that no forbidden command occurred.

## Acceptance

- `EvaluationTrace.MatchedRuleIDs` contains exactly the rules whose complete trigger predicates fired.
- Composite path-and-command rules use the same normalized command evidence for tracing and enforcement.
- Trace computation does not execute scripts or duplicate enforcement side effects.
- Table-driven regressions cover path-only, command-only, composite hit, and composite miss cases.

## Sub-Tasks

- [ ] Define one side-effect-free trigger contract shared by metrics and enforcement.
- [ ] Thread current commands into matched-rule computation.
- [ ] Add parity regressions across filtered and full checks.
- [ ] Run focused runtime trace tests.

## Notes

- Verified from finding 7.
- `matchedRuleIDs` builds a context with nil `currentCommands`; `ruleTriggerMatches` treats the composite path portion as sufficient in that path.

## Deviations
