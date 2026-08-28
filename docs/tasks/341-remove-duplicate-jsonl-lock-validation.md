# TASK 341: Remove duplicate JSONL lock validation

## Why

JSONL lock acquisition validates the same opened lock identity twice with no intervening operation that can alter the descriptor or its path binding. The second stat sequence is redundant under every append and recovery lock.

## Acceptance

- Exactly one required opened-lock identity validation remains at the correct boundary.
- Layout security validation remains separate and no symlink, replacement, ACL, ownership, or link-count check is lost.
- Fault-injection proves removal of either genuinely required check would fail a regression.
- JSONL, private-layout, ledger, audit, and race tests pass.

## Sub-Tasks

- [ ] Trace both validations and intervening calls
- [ ] Remove only the proven duplicate
- [ ] Add validation call-count and replacement coverage
- [ ] Run every JSONL consumer gate

## Notes

- Evidence: `internal/jsonl/lock.go:67-74`.

## Deviations

None.
