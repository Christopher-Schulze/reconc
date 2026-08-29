# TASK 432: Report partial wrapper installation

## Why

Hook installation can publish or update the repository wrapper and then fail while ensuring its target. The outer install API returns only an error, hiding the successful filesystem mutation from users and automation.

## Acceptance

- Every post-write failure returns a truthful partial report containing the exact changed artifact and action.
- Reports never claim rollback unless verified rollback occurred.
- Retry/remediation is explicit and idempotent.
- Failure-injection tests cover wrapper creation/update followed by target inspection, publication, and verification failures.

## Sub-Tasks

- [ ] Carry write outcomes through wrapper-target setup and outer install errors.
- [ ] Reuse the existing partial-install CLI/report contract.
- [ ] Add deterministic post-write failure tests.
- [ ] Run focused hook install and CLI tests.

## Notes

- Verified from finding 115 in `internal/hooks/hooks.go`.

## Deviations
