# TASK 322: Remove duplicate TASK move precondition validation

## Why

`publishTransactionMove` calls `validateMovePublishPrecondition` twice with identical arguments before any source or destination mutation. The second filesystem validation adds cost but no new observation boundary.

## Acceptance

- The duplicate call is removed only after proving no intervening operation can affect its postcondition.
- Source, destination, link, replacement, rollback, and transaction recovery checks remain unchanged.
- A focused call-count or fault-injection test prevents accidental loss of the required single validation.
- TASK lifecycle and self-host tests pass.

## Sub-Tasks

- [ ] Confirm both calls are semantically identical
- [ ] Remove the redundant validation
- [ ] Add focused move-publication coverage
- [ ] Run task lifecycle and self-host gates

## Notes

- Evidence: `internal/tasklifecycle/transaction.go:633-645`.

## Deviations

None.
