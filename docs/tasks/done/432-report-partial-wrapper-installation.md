# TASK 432: Report partial wrapper installation

## Why

Hook installation can publish or update the repository wrapper and then fail while ensuring its target. The outer install API returns only an error, hiding the successful filesystem mutation from users and automation.

## Acceptance

- Every post-write failure returns a truthful partial report containing the exact changed artifact and action.
- Reports never claim rollback unless verified rollback occurred.
- Retry/remediation is explicit and idempotent.
- Failure-injection tests cover wrapper creation/update followed by target inspection, publication, and verification failures.

## Sub-Tasks

- [x] Carry write outcomes through wrapper-target setup and outer install errors.
- [x] Reuse the existing partial-install CLI/report contract.
- [x] Add deterministic post-write failure tests.
- [x] Run focused hook install and CLI tests.

## Notes

- Verified from finding 115 in `internal/hooks/hooks.go`.
- Confirmed on current code: `ensureWrapper` discarded its successful wrapper action whenever `ensureWrapperTarget` failed, causing `Install` to return a nil report after a real filesystem mutation. Atomic publication also exposed a changed outcome on post-publication errors, but the hook publication adapter discarded it.
- Deterministic injected cases now cover direct-target inspection after wrapper creation, publication failure after wrapper update, and verification failure after both wrapper and receipt publication. Each case verifies the partial report, exact actions, persisted bytes, and an idempotent successful retry.
- Focused hook tests passed in 0.268 seconds and focused CLI report tests passed in 0.286 seconds. Generated-reference validation and `git diff --check` passed. Full, race, vet, lint, release-trust, and platform gates remain deferred to the requested queue-end pass.

## Deviations
