# TASK 347: Remove duplicate create-only parent validation

## Why

Create-only atomic publication validates the same parent twice consecutively without an intervening filesystem operation. The second call adds latency and maintenance noise without strengthening the invariant.

## Acceptance

- Create-only publication performs one parent validation at the duplicated location.
- All later validation boundaries required around mutations remain intact.
- Tests preserve create-only parent-replacement and security behavior.

## Sub-Tasks

- [x] Confirm the exact validation sequence and mutation boundaries.
- [x] Remove only the redundant consecutive validation.
- [x] Run focused atomic-file tests.

## Notes

- Verified from OMP session `01a04cc5-6a01-7526-95bb-b2715553daf3`, finding #92.
- Current-code verification found two consecutive `parent.validate()` calls after the temporary unlink and its directory sync in `internal/atomicfile/write_new.go`; no filesystem operation intervened between them.
- Removed only the second call. The pre-link parent validation, target validation, post-link validation, post-publication directory sync, and final post-sync parent validation remain unchanged.
- Focused verification passed with `go test ./internal/atomicfile -run 'TestWriteNewPublishesCompleteBytesAndRefusesExistingTarget|TestPublicationResultRetainsPostPublicationUncertainty|TestPublicationRejectsNestedParentSymlink|TestBoundParentDetectsDirectoryIdentitySwap|TestConcurrentPublicationsRemainWholeAndLeaveNoTemporaries'`.
- TASK 322 and TASK 341 removed different duplicate validations and do not cover this path.

## Deviations

None.
