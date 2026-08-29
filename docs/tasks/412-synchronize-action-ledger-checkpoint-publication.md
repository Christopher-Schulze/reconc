# TASK 412: Synchronize action-ledger checkpoint publication

## Why

`Recover` can rebuild and publish the in-memory checkpoint cache without `appendMu`, while `Append` reads and dereferences the same pointer under that mutex. Concurrent recovery and traffic therefore form a real Go data race.

## Acceptance

- Every checkpoint cache read and publication uses one documented synchronization boundary.
- Recovery and append remain deadlock-free across JSONL callbacks and file locks.
- A stale or partial checkpoint can never be observed or used to advance the ledger chain.
- A deterministic concurrent Recover/Append regression passes under `go test -race` when the race suite is explicitly requested.

## Sub-Tasks

- [ ] Trace checkpoint lock ordering through Recover, JSONL callbacks, Append, and publication.
- [ ] Add the smallest dedicated or existing mutex boundary without inversion.
- [ ] Add deterministic interleaving hooks and concurrent chain verification tests.
- [ ] Run focused non-race tests; retain the race regression for explicit race-suite runs.

## Notes

- Verified from finding 90 and its duplicate finding 112.
- `publishCheckpointLocked` writes `s.checkpoint`; the Recover callback reaches it without `s.appendMu`, while append fast paths read the pointer under `s.appendMu`.

## Deviations
