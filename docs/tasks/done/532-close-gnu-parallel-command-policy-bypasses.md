# TASK 532: Close GNU Parallel command-policy bypasses

## Why

GNU Parallel rebuilds an inner command after outer-shell quote removal and performs replacement expansion; treating its static outer words as a complete executable analysis can admit forbidden commands.

User-approved review item: R1. Execute on main; commit and push after completion. Product tags and releases remain outside this task.

## Acceptance

- Quoted inner commands cannot hide forbidden executables.
- Executable replacement strings and unsupported expansion modes never receive a complete analysis.
- Known safe literal arguments remain accepted; parser resource limits and existing wrapper semantics remain enforced.
- Focused behavioral tests and required repository gates pass.

## Sub-Tasks

- [x] Read the launcher and recursive shell-analysis contracts; reproduce quoted-command and executable-replacement bypasses before changing production behavior.
- [x] Separate safely identifiable command templates from unsupported replacement and dispatcher modes. Reuse bounded recursive shell analysis for static inner commands; reject unresolved executable positions as incomplete.
- [x] Preserve literal argument behavior and existing direct, quoted, nested-wrapper, and quote-mode semantics; test safe negative controls and bounded recursion.
- [x] Update the command-prevention documentation, run shell/runtime regressions and repository gates, inspect the final diff, archive, commit, and push origin/main.

## Notes

- Starting source: 70117feaa45b85d045670219d598fe9d1f1428e8.
- Source boundaries: internal/shellcommand/dispatchers.go; internal/shellcommand/shellcommand.go; internal/runtime/evaluator_match.go.
- Use isolated repository fixtures; never run repository-targeted Reconc commands against the product root.
- Planning details are explicitly tracked for this approved work despite the existing local-task ignore rule.
- Ten behavioral cases reproduced the original bypass before the repair. The complete fifteen-case runtime matrix now passes, including literal negative controls and nested launchers.
- Static Parallel templates use the existing bounded inner-shell parser; quote mode retains argument boundaries and appended input remains dynamic. Unsupported replacement languages stay incomplete.
- A separate regression proves depth-budget enforcement and uncertainty for exact matching with runtime-appended input. Existing launcher expectations now include the real inner shell and unknown input.
- Validation passed: focused runtime/shell regressions, make test (publication audit, both complete race suites, release trust), make vet, make lint, make build, make fmt-check, and git diff --check. Local logs: .build/review-repairs/task532-*.log.

## Deviations

None.
