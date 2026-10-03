# TASK 566: Bind staged CI checks to matching worktree contents

## Why

Finding F1: CI selects staged paths from the index, while native source and
evidence gates read worktree contents. A staged violation can be concealed by an
unstaged correction. Stable candidate fingerprints do not prove index/worktree
equivalence. The accepted repair is a staged-only precondition using the existing
completion snapshot, without an index checkout or a new content-provider layer.

## Acceptance

- Staging a source-hygiene violation and removing it only in the worktree blocks
  `ci --staged` before content evaluation can publish a successful decision.
- Unstaged tracked changes and untracked inputs cannot qualify as the exact
  staged candidate; a matching staged-only worktree retains existing behavior.
- Git status failure is fail-closed. Range CI keeps its current semantics.
- Text and native CI output explain the candidate mismatch and exact remediation.
- Targeted regression tests, the root Go suite, vet, lint, and development build
  pass. Documentation states the staged precondition and its partial-staging cost.
- The completed TASK is archived, committed, and pushed to origin/main.

## Sub-Tasks

- [x] Add a real isolated-Git regression for the staged/worktree content mismatch.
- [x] Enforce the existing completion snapshot's index/worktree equality in staged CI.
- [x] Cover clean staged, untracked, status-error, and unaffected range behavior; flush docs.
- [x] Run validation, review all changes, archive, commit, and push.

## Technical Plan

`prepareCIEvaluation(repo, staged, base, head, inputs)` already captures a
`CompletionStateSnapshot`. Check `GitAvailable`, `GitStatusOK`, and
`WorktreeMatchesIndex` for staged mode immediately after capture, preserving
evidence-overflow handling. Return a blocking candidate-precondition error through
the existing CI failure-report route. The precondition covers all live inputs,
including module manifests and policy inputs, rather than staged source paths
alone. Keep the existing before/after fingerprint check for concurrent mutation.

Use existing CLI Git-fixture helpers and a blocking native source-hygiene rule.
The regression must fail on the old code by receiving a successful CI decision
for the staged bad content. Add table-driven tracked/untracked mismatch cases and
positive staged-only coverage. Update docs/documentation.md in the existing Git
and command-evidence sections; do not add policy fields or dependencies.

## Notes

Audit baseline: 571546a9685d2d5700f6dafc4245ae789f5f8062. Source owners:
internal/cli/ci_preparation.go, internal/cli/ci_cmd.go,
internal/runtime/agentsession/completion_state.go, internal/assurance/files.go.
The user explicitly authorized implementation and one commit/push per TASK.
The old implementation failed the regression: both an unstaged correction of
staged TODO content and an untracked source input incorrectly returned exit 0.
The full CLI suite, targeted candidate regression, vet, staticcheck, and development
build passed. The first root suite exposed ambient HOME leakage in unrelated
global-update tests: the real installed shared skill caused fixture updates to
refuse. Root validation uses an isolated HOME while retaining the existing Go
cache/module paths; no production installation or unrelated code is changed.
The user instructed this session to omit further race checks.
Final validation: isolated-HOME make test-fast passed for the complete root and
portable-template modules; make vet, make lint, and make build passed. The final
candidate regression also passed after adding the unavailable-Git case. No new
dependency, policy field, release version, or product-repository bootstrap was used.

## Deviations

None.
