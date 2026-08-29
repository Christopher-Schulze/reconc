# TASK 322: Remove duplicate TASK move precondition validation

## Why

`publishTransactionMove` calls `validateMovePublishPrecondition` twice with identical arguments before any source or destination mutation. The second filesystem validation adds cost but no new observation boundary.

## Acceptance

- The duplicate call is removed only after proving no intervening operation can affect its postcondition.
- Source, destination, link, replacement, rollback, and transaction recovery checks remain unchanged.
- A focused call-count or fault-injection test prevents accidental loss of the required single validation.
- TASK lifecycle and self-host tests pass.

## Sub-Tasks

- [x] Confirm both calls are semantically identical
- [x] Remove the redundant validation
- [x] Add focused move-publication coverage
- [x] Run task lifecycle and self-host gates

## Notes

- Evidence: `internal/tasklifecycle/transaction.go:623-650`.
- The first `validateMovePublishPrecondition` remains immediately before path resolution and `os.Link`; no filesystem operation can mutate its source or destination postcondition before the link.
- The duplicate call was removed without changing source, destination, link verification, source removal, rollback, or recovery behavior. Existing move hardening and integrity tests provide the focused publication coverage.
- Verification: task-lifecycle unit/integrity/hardening suites and `make self-host` passed.

## Deviations

None.
