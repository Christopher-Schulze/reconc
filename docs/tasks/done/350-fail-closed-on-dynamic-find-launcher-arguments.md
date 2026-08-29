# TASK 350: Fail closed on dynamic find launcher arguments

## Why

Launcher analysis treats dynamic `find` arguments before a literal action as harmless. Such an argument can expand into `-exec` or another command-running predicate, hiding an invoked command while the analysis reports completeness.

## Acceptance

- Any unresolved `find` argument capable of changing expression structure makes launcher analysis incomplete.
- Literal safe predicates and fully visible `-exec` or `-execdir` actions retain current extraction behavior.
- Hidden-command enforcement fails closed for dynamic predicate injection.
- Tests cover variables and substitutions before, within, and after command-running actions.

## Sub-Tasks

- [x] Define the safe literal subset for `find` launcher expressions.
- [x] Mark structurally dynamic expressions incomplete.
- [x] Add adversarial hidden-command regression tests.
- [x] Run focused command-analysis and runtime tests.

## Notes

- Verified from OMP session `01a04cc5-6a01-7526-95bb-b2715553daf3`, finding #95.
- Reverified the omission: `launcherCommands` skipped dynamic `find` words without clearing its completeness result.
- The safe subset is fully literal `find` expression arguments; every dynamic argument is now structurally unknown and marks analysis incomplete while visible static `-exec` variants remain extracted.
- Regression coverage exercises dynamic values before, within, and after command-running actions and confirms the runtime shell guard blocks them.
- Focused shell-command and runtime shell-guard tests passed on macOS. Windows paths remain CI-only.

## Deviations

None.
