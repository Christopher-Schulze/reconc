# TASK 559: Preserve DSH patch edits and finish regression gates

## Why

Audit finding 7 reproduces silent loss of a modified managed DSH patch during reinstall. Findings 8-9 require complete regression and documentation propagation across the finished repair.

## Acceptance

- Reinstall refuses a drifted activation patch before changing any managed artifact, including with force; unchanged reinstall and foreign-patch refusal remain correct.
- Tests prove exact byte preservation and lack of collateral mutation for install, sync/refresh where applicable, and uninstall.
- All nine audit findings are mapped to completed implementation, tests, or precise documented support boundaries.
- Full project gates, build, isolated self-host checks, and documentation/publication checks pass; commits are pushed to origin/main without a release.

## Sub-Tasks

- [ ] Tighten patch ownership preflight and add preservation regressions.
- [ ] Re-read changed code/docs/tests and reconcile every audit finding.
- [ ] Run final gates, archive the task, commit, push, and verify remote state.

## Notes

The exact generated patch is stable; a matching header alone does not prove ownership of modified content. User-specific overlays belong in a separate patch file. Preserve current managed artifacts on every conflict. Source/offline acceptance is sufficient; no DSH host/model run belongs to these tasks.

## Deviations

None.
