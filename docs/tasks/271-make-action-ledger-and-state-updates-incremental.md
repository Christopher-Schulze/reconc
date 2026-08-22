# TASK 271: Make action ledger and state updates incremental

## Why

Every action-ledger append reloads and verifies the complete retained live file
and archive ring, rebuilds all call lifecycle states, and copies the record set.
That is O(total retained history) per gateway event. Action-state status also
re-encodes the full state only to report persisted bytes, and reservation paths
linearly search bounded collections under the state lock. The current behavior
is correct but turns long-lived histories into steadily increasing call
latency.

## Acceptance

- Ledger append cost is proportional to the new record plus the active-call
  set, not the complete retained history, after a verified checkpoint is
  available.
- The incremental checkpoint is authenticated and transactionally bound to the
  detached head, retained tail, archive rotation, key generation, repository
  identity, and lifecycle summary. Startup, recovery, external-writer change,
  checkpoint absence, and checkpoint corruption trigger full verification or
  fail closed.
- No cache trusts only path, inode, size, or modification time. Historical
  tampering with restored metadata remains detectable before a new append is
  accepted.
- Idempotent retry, sequence/digest chain, rotation protection for active calls,
  multi-process locking, crash recovery, and archive query verification remain
  exact.
- State loading makes the already validated serialized byte length available to
  `Status` without a second full marshal.
- Reservation and terminal-call lookups use canonical ordering/indexes or
  binary search while preserving persisted schema, deterministic rendering,
  limits, and clone isolation.
- Benchmarks cover empty, active, near-rotation, and maximum retained ledgers,
  plus maximum state status and reserve/release. Results include allocations and
  latency slopes, not only one fixed fixture.
- Ledger/state unit, corruption, rotation, recovery, multi-process, race,
  schema, docs, and complete gates pass.

## Sub-Tasks

- [~] Specify the authenticated incremental ledger checkpoint and migration behavior
- [ ] Add full-load checkpoint construction and corruption validation
- [ ] Update append, detached-head commit, and rotation as one recoverable transaction
- [ ] Maintain active-call lifecycle state incrementally
- [ ] Reuse validated persisted state size in status reporting
- [ ] Replace linear reservation and terminal-call lookups where canonical order permits
- [ ] Add adversarial history-tamper, external-writer, crash, rotation, and key-generation tests
- [ ] Extend scaling benchmarks and calibrated history
- [ ] Update ledger/state architecture and recovery documentation
- [ ] Run race, publication, release-trust, and complete repository verification

## Notes

- Current evidence: `internal/actionledger/store.go:Append` calls
  `loadVerifiedLocked`, copies all records, and calls `BuildCallStatuses` before
  every `jsonl.AppendTransactionContextWithLayout` operation.
- The detached head currently proves only the last sequence/digest. It is not
  sufficient by itself to preserve the existing guarantee that historical
  retained bytes were not modified.
- Current evidence: `internal/actionstate/status.go:statusFromState` calls
  `encodeBoundedJSON` solely to derive `StateBytes` after `loadState` already
  decoded bounded persisted bytes.
- Last-record-only retry behavior is intentional unless a failing caller trace
  proves a broader idempotency requirement. Do not add an unbounded historical
  idempotency index.
- Repeated private-directory and ACL validation is a security boundary. Remove
  it only where one opened identity already provides the same proof for the
  complete locked operation.

## Deviations

None.
