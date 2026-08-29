# TASK 385: Preserve stricter managed-artifact permissions

## Why

Mode reconciliation applies the exact generated mode to existing hook artifacts. Reinstalling matching content can therefore widen a user-hardened `0600` file to `0644` or a `0700` wrapper to `0755`.

## Acceptance

- Reconciliation never adds read/write/execute permissions beyond those required for the artifact to function.
- Existing stricter modes remain stable when content and required executability are valid.
- Missing owner execute permission is repaired only for executable artifacts.
- Unix and Windows mode-proxy tests cover stricter, insufficient, and unchanged modes.

## Sub-Tasks

- [ ] Define required versus preferred permissions for every managed artifact class.
- [ ] Reconcile required bits without globally weakening `atomicfile` callers.
- [ ] Add adversarial mode regressions for hook install and verify.
- [ ] Run focused hooks and atomicfile tests.

## Notes

- Verified from finding 25.
- The change must be scoped to managed-artifact policy; `atomicfile` exact-mode callers may intentionally require exact permissions.

## Deviations
