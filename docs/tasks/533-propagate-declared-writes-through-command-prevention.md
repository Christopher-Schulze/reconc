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

- [ ] Read declaration parsing, pre-command/pre-write routing, normalization, cache dependencies, and evidence persistence; reproduce the first-write discrepancy with an evaluator control.
- [ ] Parse and normalize exact declared effects before command prevention and share the combined historical/prospective write snapshot with command evaluation, dependency capture, and write prevention.
- [ ] Keep prospective paths out of durable completed-write evidence until execution is observed; preserve authority approval binding and invalid declaration rejection.
- [ ] Test matching and nonmatching triggers, absent bound rules, malformed declarations, and repeated tool IDs; update docs, run gates, archive, commit, and push origin/main.

## Notes

- Starting source: 70117feaa45b85d045670219d598fe9d1f1428e8.
- Source boundaries: internal/runtime/agentsession/handlers.go; internal/runtime/agentsession/native_approval.go; internal/runtime/agentsession/pre_decision_cache.go.
- Use isolated repository fixtures; never run repository-targeted Reconc commands against the product root.
- Planning details are explicitly tracked for this approved work despite the existing local-task ignore rule.

## Deviations

None.
