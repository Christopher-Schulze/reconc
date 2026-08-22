# TASK 262: Consolidate repository onto main

## Why

The repository accumulated one development branch and ten temporary Windows
CI branches even though ongoing work is intended to advance one canonical
`main` line. The branch tips must be audited before removal so no implemented
improvement or verification evidence is lost.

## Acceptance

- Local `main` contains the complete verified source at commit `57ff6f90` and
  all subsequent repository-rule changes through a conflict-free fast-forward.
- Every local and remote non-main branch is audited for commits and tree
  differences before deletion; the Windows branches are proven superseded
  TASK-241 snapshots rather than silently discarded work.
- The repository-local agent contract forbids creating, publishing, or
  switching branches without Christopher's explicit instruction.
- `origin/main` exactly matches local `main`, GitHub CI and CodeQL pass on that
  exact source, and no local or remote branch other than `main` remains.
- No merge commit, conflict resolution, release, tag, or unrelated source
  change is introduced.

## Sub-Tasks

- [x] Inventory every local and remote branch and its unique commits
- [x] Prove the Windows CI branches are progressive TASK-241 snapshots
- [x] Fast-forward local main to the complete verified source
- [x] Add and verify the repository-local main-only branch rule
- [x] Commit and push the rule plus complete source to origin/main
- [x] Verify GitHub CI and CodeQL on the exact main commit
- [x] Delete every local and remote non-main branch and verify the final topology
- [x] Archive the completed TASK

## Notes

- All ten Windows CI commits share parent `ef510f97` and chronological TASK-241
  snapshot authorship. The latest candidate `adde5f6c` and final `232f1b3a`
  have identical product trees; their only difference is TASK archival.
- Every older snapshot modifies a subset of the final TASK-241 file surface;
  its only path absent under the old spelling is the task detail later moved to
  `docs/tasks/done/`.
- `origin/main` is the repository's configured default branch.
- Local and remote topology now contain only `main`; all deleted branch tips
  remain reachable from the final source or are documented intermediate
  TASK-241 snapshots whose latest product tree equals the archived result.
- Main CI on `57ff6f90` proved macOS, Ubuntu, LangChain, and CodeQL, then
  correctly rejected numeric coverage measurements added to the archived TASK
  261 prose. Those measurements were removed because release trust forbids
  numeric coverage policy in project text.
- The corrected tree passes `make test-fast`, publication audit, generated
  reference verification, harness-pack verification, and the complete local
  release-trust artifact and tamper gate. The final main push is observed
  through GitHub's required CI and CodeQL checks before completion is reported.

## Deviations

None.
