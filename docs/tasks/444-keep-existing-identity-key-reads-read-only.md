# TASK 444: Keep existing identity-key reads read-only

## Why

`AcquireExistingIdentityKey` is documented as non-creating/read-only, but its shared-lock path opens the existing lock through the repairing read-write helper, which can chmod filesystem state.

## Acceptance

- Existing-key inspection never creates, chmods, repairs, or rewrites directories, locks, or key material.
- Shared locking works through the platform-supported read-only descriptor contract or fails explicitly without mutation.
- Insecure existing modes are rejected, not repaired.
- Unix and Windows tests compare metadata before/after success and failure paths.

## Sub-Tasks

- [ ] Route existing shared locks through the existing validate-only privatefs primitive.
- [ ] Verify platform lock semantics and retain bounded cancellation.
- [ ] Add non-mutation and insecure-mode regressions.
- [ ] Run focused action-state/privatefs tests.

## Notes

- Verified from finding 180; `privatefs.OpenExistingLockReadOnly` already provides the intended primitive.

## Deviations
