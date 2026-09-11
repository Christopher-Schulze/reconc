# TASK 513: Repair existing Windows integration gate failures

## Why

CI run 34579305219 at b5286a8f954cda0217415ff5d9852d5602fe22ba
already fails the full Windows suite before TASK 512. These failures are
independent of the product-version cleanup and prevent an overall green
Windows job and its later native installer step.

## Acceptance

- The native Windows full root and portable-template suites pass without
  disabling tests or weakening security and read-only guarantees.
- Generated adapter denial, read-only session inspection, supported special
  Git filenames, and native approval authority fixtures exercise real behavior.
- The same candidate reaches and passes Windows build, smoke, and installer
  steps; Linux and macOS regression gates remain green.

## Sub-Tasks

- [ ] Reproduce and attribute each recorded Windows failure against current source.
- [ ] Correct platform behavior or demonstrably invalid fixtures with focused tests.
- [ ] Run native Windows and cross-platform gates, document, archive, commit, and push.

## Notes

- Source evidence: https://github.com/Christopher-Schulze/reconc/actions/runs/34579305219
  (`Windows build and smoke`, `Test full Windows suite`).
- `internal/cli/hook_scenario_e2e_test.go`: all eight
  `TestGeneratedAdaptersExecuteTemplateDenial` variants exit 1 without their
  expected denial envelope.
- `internal/cli/session_briefing_inspection_test.go`: four inspection tests
  observe changed `sessions/.../locks` directory modification times.
- `internal/runtime/git_test.go`: the special-name rename fixture attempts
  to create a tab-containing filename rejected by Windows.
- `internal/runtime/agentsession/native_approval_test.go`: four approval
  cases stop at `private Windows DACL is not protected` before their intended
  decision boundary.
- TASK 512 does not repair these paths. Do not infer a green Windows gate from
  local macOS validation or from passing version-identity tests.

## Deviations

None.
