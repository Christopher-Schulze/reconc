# TASK 403: Surface corrupt and drifted init state truthfully

## Why

Recorded init discovery silently ignores unreadable, malformed, root-mismatched, or receipt-invalid plans and can proceed as though no transaction existed. After a successful apply, a second verification failure is reported as `rolled_back` even though installed artifacts remain on disk.

## Acceptance

- Missing recorded state is distinguished from unreadable, malformed, tampered, or root-mismatched state.
- Init refuses parallel state creation when an existing candidate cannot be safely classified.
- Post-apply drift uses a truthful status and remediation unless an actual verified rollback occurred.
- Deterministic tests inject read failures, digest tampering, multiple candidates, and mutation between apply and verify.

## Sub-Tasks

- [x] Define typed recorded-plan inspection outcomes.
- [x] Propagate non-absence errors through init without mutation.
- [x] Correct post-apply report status and JSON documentation.
- [x] Run focused bootstrap init tests.

## Notes

- Verified from findings 69 and 70.
- Before the fix, recorded-plan discovery continued past every load or receipt error, and post-apply verification assigned `rolled_back` without invoking rollback.
- Recorded discovery now returns typed `absent`, `valid`, or `invalid` state and refuses unreadable, malformed, tampered, foreign-root, receipt-invalid, or multiple candidates.
- Init and repository sync propagate invalid recorded state instead of treating it as absence, and init returns a concrete repair-first remediation without creating parallel state.
- A deterministic post-apply hook proves real filesystem mutation between apply and verify reports `drift`, retains the installed receipt, and exposes the failing check.
- Apply failures report `refused` before mutation, `rolled_back` only with verified removed paths, and `drift` when rollback is incomplete.
- `docs/documentation.md` now defines all four stable init JSON statuses and their mutation guarantees.
- Verification: focused bootstrap and CLI tests, `make test-fast`, and `git diff --check` passed on macOS.

## Deviations
