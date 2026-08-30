# TASK 445: Add concurrent action-state read paths

## Why

`Status` and `CurrentStateVersion` acquire the exclusive transaction writer lock even when state is healthy. Frequent gateway reads serialize with reservations and approvals, and crash recovery is mixed into the common read path.

## Acceptance

- Healthy read-only status/version calls can proceed concurrently under a validated shared lock.
- Detected recovery state escalates safely to the exclusive transaction path and then revalidates before publication.
- Readers never observe an uncommitted transaction or bypass repository/key identity checks.
- Deterministic concurrency tests and benchmarks cover healthy reads, writer contention, recovery, cancellation, and starvation.

## Sub-Tasks

- [x] Separate healthy snapshot validation from mutation-capable recovery.
- [x] Add shared-read then exclusive-escalation flow without lock upgrade deadlock.
- [x] Add concurrency/regression benchmarks.
- [x] Run focused action-state tests and benchmarks.

## Notes

- Verified from finding 181 in `internal/actionstate/status.go`, `budget_store.go`, and `store.go`.
- `Status` and `CurrentStateVersion` now retain the key lease and a shared state lock while validating healthy snapshots. Any journal entry causes the reader to release both leases before entering the existing exclusive recovery path, preventing lock-upgrade and key-lease writer deadlocks.
- Parallel healthy-version reads improved from about 321 us/op to 131 us/op on the focused 8-worker benchmark, with unchanged allocation count. Deterministic tests prove reader overlap, bounded writer contention, writer progress after release, cancellation, and recovery escalation.

## Deviations
