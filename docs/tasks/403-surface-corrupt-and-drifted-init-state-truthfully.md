# TASK 403: Surface corrupt and drifted init state truthfully

## Why

Recorded init discovery silently ignores unreadable, malformed, root-mismatched, or receipt-invalid plans and can proceed as though no transaction existed. After a successful apply, a second verification failure is reported as `rolled_back` even though installed artifacts remain on disk.

## Acceptance

- Missing recorded state is distinguished from unreadable, malformed, tampered, or root-mismatched state.
- Init refuses parallel state creation when an existing candidate cannot be safely classified.
- Post-apply drift uses a truthful status and remediation unless an actual verified rollback occurred.
- Deterministic tests inject read failures, digest tampering, multiple candidates, and mutation between apply and verify.

## Sub-Tasks

- [ ] Define typed recorded-plan inspection outcomes.
- [ ] Propagate non-absence errors through init without mutation.
- [ ] Correct post-apply report status and JSON documentation.
- [ ] Run focused bootstrap init tests.

## Notes

- Verified from findings 69 and 70.
- `recordedInitPlan` currently `continue`s on every load/receipt error; `initializeLocked` assigns `InitRolledBack` without invoking rollback.

## Deviations
