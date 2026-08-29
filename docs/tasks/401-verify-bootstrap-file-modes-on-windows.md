# TASK 401: Verify bootstrap file modes on Windows

## Why

Bootstrap mode verification returns true for every regular file on Windows. This conflicts with `atomicfile`'s Windows reconciliation, which uses the owner-write bit as the read-only attribute proxy.

## Acceptance

- Windows bootstrap verification compares the supported writable/read-only mode proxy consistently with atomic publication.
- A matching-content read-only mismatch is reported and repaired rather than classified unchanged.
- Unix exact permission behavior remains unchanged.
- Platform-specific tests cover writable, read-only, executable-intent, conflict, verify, and apply paths; Windows tests are maintained for CI but not required locally.

## Sub-Tasks

- [ ] Read the Windows mode publication and verification contracts end to end.
- [ ] Share or exactly mirror the supported mode predicate.
- [ ] Add Windows-specific and platform-neutral plan regressions.
- [ ] Run focused non-Windows tests and leave Windows execution to CI.

## Notes

- Verified from finding 67.
- `modeSatisfies` currently reduces Windows verification to `actual.IsRegular()`, already checked by callers.

## Deviations
