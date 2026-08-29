# TASK 341: Remove duplicate JSONL lock validation

## Why

JSONL lock acquisition validates the same opened lock identity twice with no intervening operation that can alter the descriptor or its path binding. The second stat sequence is redundant under every append and recovery lock.

## Acceptance

- Exactly one required opened-lock identity validation remains at the correct boundary.
- Layout security validation remains separate and no symlink, replacement, ACL, ownership, or link-count check is lost.
- Fault-injection proves removal of either genuinely required check would fail a regression.
- JSONL, private-layout, ledger, audit, and race tests pass.

## Sub-Tasks

- [x] Trace both validations and intervening calls
- [x] Remove only the proven duplicate
- [x] Add validation and replacement coverage
- [x] Run every JSONL consumer gate

## Notes

- Evidence: `internal/jsonl/lock.go:67-74`.
- The post-acquisition `validateOpenedLayoutLock` call remains immediately before the independent layout-security check. The second call had no mutating operation between it and the first and was removed; open-time identity, mode, symlink, owner, ACL, link-count, replacement, and security checks remain intact.
- Existing creation/replacement and private-layout fault-injection tests cover the retained identity boundary. Verified with `go test ./internal/jsonl ./internal/privatefs ./internal/actionledger ./internal/audit ./internal/retention -count=1`.

## Deviations

None.
