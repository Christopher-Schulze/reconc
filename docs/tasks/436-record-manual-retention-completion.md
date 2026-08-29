# TASK 436: Record manual retention completion

## Why

A successful manual non-dry retention run does not update the due marker, so the next hook immediately repeats the complete scan.

## Acceptance

- Successful manual and periodic real runs update one durable due marker under the same completion rules.
- Dry runs and failed/partial runs never postpone required maintenance.
- Concurrent manual and periodic runs cannot move the marker backward or mask an error.
- Deterministic clock tests cover success, dry-run, failure, concurrency, and immediate follow-up.

## Sub-Tasks

- [ ] Centralize successful-run marker publication.
- [ ] Bind marker time to the completed scan and existing retention lock.
- [ ] Add scheduling and failure regressions.
- [ ] Run focused retention tests.

## Notes

- Verified from finding 154 in `internal/retention/prune.go`.

## Deviations
