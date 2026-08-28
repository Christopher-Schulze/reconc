# TASK 337: Report atomic-file post-publication outcomes truthfully

## Why

Atomic replacement and create-only writes can publish the target, then fail parent validation, temporary cleanup, or directory sync. Callers receive an error and sometimes `changed=false` without a structured indication that publication already occurred, making retries and rollback ambiguous.

## Acceptance

- Atomic-file APIs distinguish not-published, published-but-durability-uncertain, and durably-published outcomes.
- Privatefs and every caller preserve that outcome instead of collapsing it to `changed=false`.
- Create-only retry and rollback behavior is deterministic after each post-publication failure.
- Fault injection covers replacement, hardlink creation, temp removal, parent validation, sync, and close errors.

## Sub-Tasks

- [ ] Inventory atomic-file outcome contracts and callers
- [ ] Define the smallest typed publication result
- [ ] Propagate post-publication state through privatefs consumers
- [ ] Add failure-at-every-boundary and retry tests

## Notes

- Evidence: `internal/atomicfile/write.go`, `write_new.go`, and `write_stream.go` post-publication error paths.

## Deviations

None.
