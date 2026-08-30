# TASK 436: Record manual retention completion

## Why

A successful manual non-dry retention run does not update the due marker, so the next hook immediately repeats the complete scan.

## Acceptance

- Successful manual and periodic real runs update one durable due marker under the same completion rules.
- Dry runs and failed/partial runs never postpone required maintenance.
- Concurrent manual and periodic runs cannot move the marker backward or mask an error.
- Deterministic clock tests cover success, dry-run, failure, concurrency, and immediate follow-up.

## Sub-Tasks

- [x] Centralize successful-run marker publication.
- [x] Bind marker time to the completed scan and existing retention lock.
- [x] Add scheduling and failure regressions.
- [x] Run focused retention tests.

## Notes

- Verified from finding 154 in `internal/retention/prune.go`.
- The finding was current: `Run` omitted `.last-retention`, while `RunIfDue` advanced it even when the scan reported errors.
- Manual and due real runs now share one bounded, canonical timestamp marker publication path under `.retention.lock`; older concurrent completion times cannot replace newer evidence.
- Dry runs, failed scans, and marker-publication failures leave the due state unchanged, so an immediate follow-up remains due.
- Focused tests passed: `go test ./internal/retention -count=1`; `go test ./internal/cli -run 'TestPrune' -count=1`; final scheduling/failure subsets passed after review.

## Deviations
