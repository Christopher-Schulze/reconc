# TASK 420: Define deterministic repository control-directory permissions

## Why

Bootstrap creates missing repository artifact parents with `0755`, while private atomic publication creates missing parents with `0700`. The resulting `.reconc` accessibility therefore depends on which command created it first and on the process umask, even though the directory mixes shareable repository artifacts with private transaction state and a run-decision log containing session identifiers.

## Acceptance

- `.reconc`, `.reconc/run`, its JSONL data/lock/archive files, and each other security-sensitive subdirectory have one documented cross-platform access contract based on their actual contents.
- Creation order and umask cannot change the effective supported access class.
- Existing directories are never widened or narrowed without identity, ownership, shared-repository compatibility, and user-data checks.
- Unix mode and Windows ACL tests cover compiler-first, bootstrap-first, existing stricter modes, shared repositories, and mixed public/private artifacts.

## Sub-Tasks

- [x] Inventory every `.reconc` directory creator and the sensitivity/shareability of its direct children.
- [x] Choose explicit root and private-subdirectory boundaries without moving or exposing existing state.
- [x] Reconcile creation paths and document the guarantee.
- [x] Add platform-specific order/permission regressions and run focused tests.

## Notes

- Verified from findings 71 and 196.
- `bootstrap/root.go` requests `0755`; `atomicfile.WritePrivateIfChanged` requests private parent creation for the compiler lockfile path. Neither contract makes creation-order differences intentional.
- A blanket `0700` root or blanket `0755` root is not preselected because `.reconc` contains both repository-visible and private lifecycle artifacts.
- Verified creators and callers: compiler locking, bootstrap parent creation, TASK lifecycle locking, audit publication, repository-run state/JSONL, and retention inspection.
- Final contract: a new Unix `.reconc` root is exact `0755`; Windows inherits the repository ACL. Existing roots are identity-validated and preserved. Public coordination children inherit group access and Unix setgid where configured. `.reconc/run` is `0700` with `0600` state and JSONL artifacts; existing exposed run directories fail closed untouched. Audit files remain stable direct children of the public root and receive `0600` or a protected current-user-only Windows DACL before publication.
- Focused Unix tests passed for compiler-first, bootstrap-first, restrictive umask, existing `0700`, shared/setgid roots, audit mixed access, private run state/JSONL, retention, atomic pre-publication security, and TASK locking. Native Windows ACL regressions cover compiler-first, bootstrap-first, inherited roots, existing protected roots, and mixed public/private artifacts; they were maintained but not run locally by explicit instruction.
- `git diff --check` passed.

## Deviations

- `make test-fast TEST_PARALLELISM=8` passed formatting and generated-reference checks and reported no assertion failure before it was interrupted after three minutes under the explicit short-run instruction. Existing long-running `actionstate` completed in 101.144 seconds; `audit` and `bootstrap` were still running when interrupted. Race, release-trust, vet, lint, full, and local Windows gates were intentionally deferred to the requested queue-end or explicit-run boundary.
