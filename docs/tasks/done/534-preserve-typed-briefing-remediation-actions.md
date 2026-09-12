# TASK 534: Preserve typed briefing remediation actions

## Why

The briefing converts a shell action into one argv element and quotes the entire command as an executable name, losing the typed execution contract.

User-approved review item: R3. Execute on main; commit and push after completion. Product tags and releases remain outside this task.

## Acceptance

- A shell action such as go test ./... remains a shell command, not a single executable name.
- Argument boundaries, working directory, and authorization requirements remain truthful.
- Unsupported or non-executable actions are never presented as directly executable.
- CLI/FixPlan regressions and repository gates pass.

## Sub-Tasks

- [x] Read FixPlan construction and briefing callers; reproduce the incorrectly quoted shell action.
- [x] Preserve the selected RemediationAction through rendering; distinguish literal shell text from separately quoted argv arguments.
- [x] Honor working directory and operator authorization in the next-action guidance without creating parallel action schemas.
- [x] Test shell, argv, quoting, foreign working directory, and non-executable actions; update docs, run gates, archive, commit, and push origin/main.

## Notes

- Starting source: 70117feaa45b85d045670219d598fe9d1f1428e8.
- Source boundaries: internal/cli/workflow_cmd.go; internal/cli/exec_cmd.go; internal/runtime/remediation.go.
- Use isolated repository fixtures; never run repository-targeted Reconc commands against the product root.
- Planning details are explicitly tracked for this approved work despite the existing local-task ignore rule.
- A real changed Git candidate, recorded write, blocking Stop report, and session-briefing invocation reproduced the quoted-program-name defect and missing execution metadata.
- Briefing selection preserves the existing RemediationAction type. The additive remediation_action field retains exact machine execution metadata; text labels distinguish shell scripts from quoted argv and display cwd and authorization.
- Non-executable, empty, or contradictory action payloads are not selected. Execution regressions verify literal arguments, apostrophes, shell pipelines, and a separate working directory without running any recommended product command.
- Validation passed: focused CLI regressions (including race execution), complete make test with both uncached race suites and release trust, make vet, make lint, make build, and git diff --check. Logs: .build/review-repairs/task534-*.log.

## Deviations

None.
