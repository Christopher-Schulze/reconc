# TASK 174: Recover failed transactional JSONL commits

## Why

`internal/jsonl/jsonl.go` publishes the live record and persists a `published`
transaction journal before invoking the caller's commit callback. If that
callback fails, `appendLockedWithLayout` returns without rolling back or
resolving the journal. Generic recovery without the callback then refuses the
published transactional journal, leaving enforcement and subsequent appends
wedged. The live record and caller-owned head can remain inconsistent.

## Acceptance

- A commit callback failure has a documented atomic outcome: either both the
  live append and callback state are recoverable, or the live append is rolled
  back without losing the prior archive ring.
- Recovery behavior is defined for callback-aware and callback-free entry
  points; no persisted state can require an unavailable callback forever.
- Fault-injection tests cover commit failure before and after rotation, process
  restart, repeated recovery, and cleanup failure while preserving modes,
  digests, archive order, and exactly-once commit semantics.
- Existing JSONL layout, security, size, and concurrency tests pass under the
  race detector.

## Sub-Tasks

- [x] Specify transactional journal states and recovery ownership
- [x] Implement an atomic commit-failure transition
- [x] Add restart and rotation fault-injection tests
- [x] Verify all JSONL transactional callers and recovery entry points
- [x] Run focused, race, and complete Go gates

## Notes

- Verified at `appendLockedWithLayout` and
  `recoverAppendLockedWithLayout` in `internal/jsonl/jsonl.go`.
- Do not treat the live append as committed merely because its bytes exist.
  Caller-owned head or ledger state is part of the transaction contract.
- A callback error is an ambiguous publication outcome: atomic head publishers
  may report a post-publication sync or validation failure. Rolling the JSONL
  record back at that point would corrupt the detached-head relationship.
- Journal v2 therefore distinguishes `published` (live bytes durable, callback
  not entered) from `committing` (callback entered, outcome may be ambiguous).
  Callback-free recovery rolls back only `published`; owner recovery retries
  `committing` with the required idempotent callback. A successful callback is
  persisted as `resolved` before artifact cleanup so cleanup-only recovery never
  invokes it again. Version-1 `published` remains callback-owned because its
  historical state cannot prove whether commit started.
- Caller audit: action-ledger append, recovery, verification, and snapshots
  always provide `commitHeadLocked`; audit append, tail, stats, export,
  verification, and retention route through `recoverPendingAppend` with the
  deterministic chain-head rebuild callback. Generic retention is not an owner
  of either chained format and remains fail-closed for ambiguous legacy or
  `committing` transactions.
- Verification: `go test ./internal/jsonl ./internal/audit
  ./internal/actionledger`, the same packages under `go test -race`, and the
  repository's complete `make test` gate all pass. The full gate includes
  formatting, publication audit, harness-pack verification, root and harness
  race suites, and release-trust failure-path tests.

## Deviations

None.
