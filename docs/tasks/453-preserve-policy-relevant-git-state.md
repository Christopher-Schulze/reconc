# TASK 453: Preserve policy-relevant Git state

## Why

Stop fingerprinting removes every path below `.reconc/`, including tracked files a policy may intentionally select. The `RECONC_STOP_FINGERPRINT_UNTRACKED=no` optimization is also reused by terminal TASK completion, allowing untracked TASK control files to disappear from the commit gate.

## Acceptance

- Git filtering excludes only enumerated Reconc-owned runtime artifacts, not the complete `.reconc` subtree.
- Terminal completion always captures untracked files with the strict all-files mode regardless of cache-fingerprint tuning.
- Cache and terminal snapshots are separate, explicit contracts.
- Adversarial tests cover tracked policy inputs under `.reconc`, untracked TASK files, ignored runtime artifacts, renames, and every fingerprint mode.

## Sub-Tasks

- [ ] Inventory exact Reconc-owned runtime paths eligible for fingerprint exclusion.
- [ ] Separate cache-cost configuration from terminal Git truth.
- [ ] Add path-policy and completion-gate regressions.
- [ ] Run focused Stop, task-lifecycle, and Git tests.

## Notes

- Verified from findings 194 and 195.

## Deviations
