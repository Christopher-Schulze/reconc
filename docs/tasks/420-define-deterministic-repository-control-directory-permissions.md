# TASK 420: Define deterministic repository control-directory permissions

## Why

Bootstrap creates missing repository artifact parents with `0755`, while private atomic publication creates missing parents with `0700`. The resulting `.reconc` accessibility therefore depends on which command created it first and on the process umask, even though the directory mixes shareable repository artifacts with private transaction state and a run-decision log containing session identifiers.

## Acceptance

- `.reconc`, `.reconc/run`, its JSONL data/lock/archive files, and each other security-sensitive subdirectory have one documented cross-platform access contract based on their actual contents.
- Creation order and umask cannot change the effective supported access class.
- Existing directories are never widened or narrowed without identity, ownership, shared-repository compatibility, and user-data checks.
- Unix mode and Windows ACL tests cover compiler-first, bootstrap-first, existing stricter modes, shared repositories, and mixed public/private artifacts.

## Sub-Tasks

- [ ] Inventory every `.reconc` directory creator and the sensitivity/shareability of its direct children.
- [ ] Choose explicit root and private-subdirectory boundaries without moving or exposing existing state.
- [ ] Reconcile creation paths and document the guarantee.
- [ ] Add platform-specific order/permission regressions and run focused tests.

## Notes

- Verified from findings 71 and 196.
- `bootstrap/root.go` requests `0755`; `atomicfile.WritePrivateIfChanged` requests private parent creation for the compiler lockfile path. Neither contract makes creation-order differences intentional.
- A blanket `0700` root or blanket `0755` root is not preselected because `.reconc` contains both repository-visible and private lifecycle artifacts.

## Deviations
