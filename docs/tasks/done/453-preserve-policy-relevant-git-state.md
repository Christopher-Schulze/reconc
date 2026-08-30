# TASK 453: Preserve policy-relevant Git state

## Why

Stop fingerprinting removes every path below `.reconc/`, including tracked files a policy may intentionally select. The `RECONC_STOP_FINGERPRINT_UNTRACKED=no` optimization is also reused by terminal TASK completion, allowing untracked TASK control files to disappear from the commit gate.

## Acceptance

- Git filtering excludes only enumerated Reconc-owned runtime artifacts, not the complete `.reconc` subtree.
- Terminal completion always captures untracked files with the strict all-files mode regardless of cache-fingerprint tuning.
- Cache and terminal snapshots are separate, explicit contracts.
- Adversarial tests cover tracked policy inputs under `.reconc`, untracked TASK files, ignored runtime artifacts, renames, and every fingerprint mode.

## Sub-Tasks

- [x] Inventory exact Reconc-owned runtime paths eligible for fingerprint exclusion.
- [x] Separate cache-cost configuration from terminal Git truth.
- [x] Add path-policy and completion-gate regressions.
- [x] Run focused Stop, task-lifecycle, and Git tests.

## Notes

- Verified from findings 194 and 195.
- Replaced the blanket `.reconc/` exclusion with exact runtime namespaces and generated artifact families. Compiled policy, install receipts, runtime manifests, scripts, and arbitrary policy inputs under `.reconc/` remain bound.
- Rename/copy filtering now preserves the complete porcelain record pair whenever either side is user-owned, preventing record corruption while excluding runtime-only moves.
- Stop evaluation stability still compares the cache-tuned snapshot. The terminal committed-TASK gate takes a separate fresh all-untracked snapshot, so `RECONC_STOP_FINGERPRINT_UNTRACKED=no` cannot hide untracked TASK files.
- Adversarial path, rename, every-mode tracked-input, cache-versus-terminal, cached completion, drift, and completion-state tests passed. Focused runs completed in 1.10 and 2.91 seconds.

## Deviations

- Per user direction, full module, race, vet, lint, release, and platform gates are deferred until TASK 460 so they run once over the final queue state.
