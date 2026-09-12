# TASK 533: Propagate declared writes through command prevention

## Why

The pre-command check currently sees only historical writes; exact declared prospective command effects are parsed later and do not activate applicable path-triggered command rules.

User-approved review item: R2. Execute on main; commit and push after completion. Product tags and releases remain outside this task.

## Acceptance

- PreToolUse and direct evaluation agree for the same command and effective write paths.
- Policy-dependent cache identities observe all declared prospective writes.
- Malformed declarations fail closed and declarations never fabricate completed evidence.
- Existing approval and command routing remains compatible; focused and full gates pass.

## Sub-Tasks

- [x] Read declaration parsing, pre-command/pre-write routing, normalization, cache dependencies, and evidence persistence; reproduce the first-write discrepancy with an evaluator control.
- [x] Parse and normalize exact declared effects before command prevention and share the combined historical/prospective write snapshot with command evaluation, dependency capture, and write prevention.
- [x] Keep prospective paths out of durable completed-write evidence until execution is observed; preserve authority approval binding and invalid declaration rejection.
- [x] Test matching and nonmatching triggers, absent bound rules, malformed declarations, and repeated tool IDs; update docs, run gates, archive, commit, and push origin/main.

## Notes

- Starting source: 70117feaa45b85d045670219d598fe9d1f1428e8.
- Source boundaries: internal/runtime/agentsession/handlers.go; internal/runtime/agentsession/native_approval.go; internal/runtime/agentsession/pre_decision_cache.go.
- Use isolated repository fixtures; never run repository-targeted Reconc commands against the product root.
- Planning details are explicitly tracked for this approved work despite the existing local-task ignore rule.
- Command prevention and pre-write checks now use the same historical/prospective write snapshot. Approval consumption still binds to the original completed session state and exact declaration.
- Cache version 5 invalidates older decisions; command dependency reachability uses normalized declared paths, while raw declared paths bind filesystem identity and retargeting.
- Focused race tests passed for evaluator parity with/without authority rules, repeated tool IDs, changed/missing evidence, read-only commands with declared writes, malformed declarations, symlink retargeting, and existing native approval behavior. Pre-execution calls leave completed-write/command evidence unchanged.
- Validation passed: focused race regressions, make test (publication audit, complete root and portable-template race suites, release trust), make vet, make lint, make build, and git diff --check. Local logs: .build/review-repairs/task533-*.log.

## Deviations

None.
