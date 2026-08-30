# TASK 412: Synchronize action-ledger checkpoint publication

## Why

`Recover` can rebuild and publish the in-memory checkpoint cache without `appendMu`, while `Append` reads and dereferences the same pointer under that mutex. Concurrent recovery and traffic therefore form a real Go data race.

## Acceptance

- Every checkpoint cache read and publication uses one documented synchronization boundary.
- Recovery and append remain deadlock-free across JSONL callbacks and file locks.
- A stale or partial checkpoint can never be observed or used to advance the ledger chain.
- A deterministic concurrent Recover/Append regression passes under `go test -race` when the race suite is explicitly requested.

## Sub-Tasks

- [x] Trace checkpoint lock ordering through Recover, JSONL callbacks, Append, and publication.
- [x] Add the smallest dedicated or existing mutex boundary without inversion.
- [x] Add deterministic interleaving hooks and concurrent chain verification tests.
- [x] Run focused non-race tests; retain the race regression for explicit race-suite runs.

## Notes

- Verified from finding 90 and its duplicate finding 112.
- Confirmed on current source: `publishCheckpointLocked` writes `s.checkpoint`; the Recover callback reaches it without `s.appendMu`, while append fast paths read the pointer under `s.appendMu`.
- Both paths acquire the JSONL file lock after their local preflight. Taking `appendMu` in `Recover` after that preflight preserves the existing `appendMu -> JSONL lock` order and never reacquires it from a JSONL callback.
- The regression blocks recovery after detached-head publication, verifies that recovery owns the checkpoint mutex, advances Append to the same mutex boundary through a deterministic hook, then releases both operations and verifies the complete chain.
- Focused recovery regressions, the complete `internal/actionledger` suite, and `make test-fast` pass. The race regression is retained but was not executed because race runs are reserved for the explicit queue-end gate.

## Deviations
