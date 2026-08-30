# TASK 401: Verify bootstrap file modes on Windows

## Why

Bootstrap mode verification returns true for every regular file on Windows. This conflicts with `atomicfile`'s Windows reconciliation, which uses the owner-write bit as the read-only attribute proxy.

## Acceptance

- Windows bootstrap verification compares the supported writable/read-only mode proxy consistently with atomic publication.
- A matching-content read-only mismatch is reported and repaired rather than classified unchanged.
- Unix exact permission behavior remains unchanged.
- Platform-specific tests cover writable, read-only, executable-intent, conflict, verify, and apply paths; Windows tests are maintained for CI but not required locally.

## Sub-Tasks

- [x] Read the Windows mode publication and verification contracts end to end.
- [x] Share or exactly mirror the supported mode predicate.
- [x] Add Windows-specific and platform-neutral plan regressions.
- [x] Run focused non-Windows tests and leave Windows execution to CI.

## Notes

- Verified from finding 67.
- Before the fix, `modeSatisfies` reduced Windows verification to `actual.IsRegular()`, already checked by callers.
- Confirmed `internal/atomicfile/mode_windows.go` treats owner-write as the only representable writable/read-only boundary; bootstrap now mirrors that exact predicate.
- Native Windows coverage exercises writable, read-only, executable-intent, matching-content conflict planning, candidate publication, and verification. It remains CI-only as requested.
- Unix regression coverage proves exact permission comparison remains unchanged.
- `docs/documentation.md` already documents the Windows readonly-attribute proxy and non-destructive bootstrap candidate contract, so no user-facing documentation change was required.
- Verification: focused bootstrap tests and `make test-fast` passed on macOS; `git diff --check` passed.

## Deviations
