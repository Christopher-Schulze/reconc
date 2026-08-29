# TASK 445: Add concurrent action-state read paths

## Why

`Status` and `CurrentStateVersion` acquire the exclusive transaction writer lock even when state is healthy. Frequent gateway reads serialize with reservations and approvals, and crash recovery is mixed into the common read path.

## Acceptance

- Healthy read-only status/version calls can proceed concurrently under a validated shared lock.
- Detected recovery state escalates safely to the exclusive transaction path and then revalidates before publication.
- Readers never observe an uncommitted transaction or bypass repository/key identity checks.
- Deterministic concurrency tests and benchmarks cover healthy reads, writer contention, recovery, cancellation, and starvation.

## Sub-Tasks

- [ ] Separate healthy snapshot validation from mutation-capable recovery.
- [ ] Add shared-read then exclusive-escalation flow without lock upgrade deadlock.
- [ ] Add concurrency/regression benchmarks.
- [ ] Run focused action-state tests and benchmarks.

## Notes

- Verified from finding 181 in `internal/actionstate/status.go`, `budget_store.go`, and `store.go`.

## Deviations
