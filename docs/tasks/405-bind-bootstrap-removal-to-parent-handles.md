# TASK 405: Bind bootstrap removal to parent handles

## Why

Removal durability reopens the parent by pathname after a bound deletion, and repository-sync rollback verifies a target then removes it by pathname. Concurrent parent or leaf replacement can redirect the durability operation or unlink a different object.

## Acceptance

- Deletion, identity revalidation, and parent durability operate through the same bound parent handle.
- Rollback never unlinks a pathname whose current object differs from the verified after-image.
- Unix fsync and Windows write-through behavior are explicit and tested to the strongest supported contract.
- Deterministic race hooks cover parent rename/symlink replacement and leaf replacement before remove and sync.

## Sub-Tasks

- [ ] Reuse the existing `openCreatedParent` and bound removal machinery for rollback.
- [ ] Carry bound parents into removal durability instead of reopening lexical paths.
- [ ] Add Unix and Windows platform regressions plus deterministic TOCTOU tests.
- [ ] Run focused bootstrap removal and repository-sync tests.

## Notes

- Verified from findings 73, 74, and 141.
- Forward apply already has fd-bound removal primitives; recovery must use the same identity contract.

## Deviations
