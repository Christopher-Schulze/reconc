# TASK 405: Bind bootstrap removal to parent handles

## Why

Removal durability reopens the parent by pathname after a bound deletion, and repository-sync rollback verifies a target then removes it by pathname. Concurrent parent or leaf replacement can redirect the durability operation or unlink a different object.

## Acceptance

- Deletion, identity revalidation, and parent durability operate through the same bound parent handle.
- Rollback never unlinks a pathname whose current object differs from the verified after-image.
- Unix fsync and Windows write-through behavior are explicit and tested to the strongest supported contract.
- Deterministic race hooks cover parent rename/symlink replacement and leaf replacement before remove and sync.

## Sub-Tasks

- [x] Reuse the existing `openCreatedParent` and bound removal machinery for rollback.
- [x] Carry bound parents into removal durability instead of reopening lexical paths.
- [x] Add Unix and Windows platform regressions plus deterministic TOCTOU tests.
- [x] Run focused bootstrap removal and repository-sync tests.

## Notes

- Verified from findings 73, 74, and 141.
- Forward apply already has fd-bound removal primitives; recovery must use the same identity contract.
- Confirmed in current code: bootstrap removal revalidated by pathname, called `os.Remove`, then reopened the parent for Unix durability; repository-sync rollback hashed a created after-image and later unlinked its pathname.
- Both paths now capture the parent and file identity, revalidate digest and mode through the opened file, delete relative to the same `os.Root`, and retain the bound parent through durability validation.
- Deterministic post-bind hooks prove leaf and parent replacement cannot redirect either deletion. A parent-sync failure after successful deletion is recorded as applied and rolls back the removed bytes.
- Post-delete validation checks through the same bound parent that the leaf remains absent before durability completes; leaf recreation or parent replacement in the remove-to-sync window fails closed without touching the replacement.
- Unix tests require the exact bound directory handle to reach fsync. Windows keeps the same handle and explicitly uses the strongest supported contract: post-delete identity validation, with write-through reserved for rollback replacement because `os.Root` exposes no directory flush or delete write-through primitive.
- Verification passed: focused bootstrap removal/repository-sync race tests, `make test-fast`, `gofmt`, and `git diff --check`. Windows-specific tests were maintained but not executed locally per queue policy.

## Deviations
