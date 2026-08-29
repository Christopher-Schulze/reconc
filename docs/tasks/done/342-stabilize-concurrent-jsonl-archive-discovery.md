# TASK 342: Stabilize concurrent JSONL archive discovery

## Why

Concurrent first writers can publish the private JSONL lock through competing
secured candidates. While one writer owns the lock, another contender may still
remove its losing candidate, changing the parent directory during archive
discovery and causing a valid append to fail closed.

## Acceptance

- Bounded archive discovery tolerates only the transient directory-snapshot
  churn caused by concurrent lock publication and still fails closed when a
  stable snapshot cannot be obtained.
- Concurrent action-ledger writers pass repeatedly without losing records or
  weakening archive-index, identity, or regular-file validation.
- The behavior, bound, and regression evidence are documented.
- Relevant JSONL and action-ledger tests plus repository gates pass.

## Sub-Tasks

- [x] Confirm the failing call path and its bounded retry contract
- [x] Implement the minimal stable archive-discovery change
- [x] Add deterministic regression coverage
- [x] Update architecture and run focused gates

## Notes

- Initial evidence: `go test ./internal/actionledger -run '^TestStoreSerializesConcurrentWriters$' -count=1 -v` reproduced `directory snapshot changed` from `jsonl.archiveCandidates`.
- The race is limited to competing secured lock publishers creating/removing
  same-directory candidates before lock acquisition. `readArchiveDirectory`
  retries only `boundedio.ErrDirectorySnapshotChanged` for 400 attempts at 5 ms
  each, then returns the original strict error.
- Focused evidence: `go test ./internal/jsonl ./internal/actionledger ./internal/audit ./internal/retention -count=1`; the action-ledger concurrency regression passed 50 repeated runs.

## Deviations

None.
