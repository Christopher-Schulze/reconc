# TASK 337: Report atomic-file post-publication outcomes truthfully

## Why

Atomic replacement and create-only writes can publish the target, then fail parent validation, temporary cleanup, or directory sync. Callers receive an error and sometimes `changed=false` without a structured indication that publication already occurred, making retries and rollback ambiguous.

## Acceptance

- Atomic-file APIs distinguish not-published, published-but-durability-uncertain, and durably-published outcomes.
- Privatefs and every caller preserve that outcome instead of collapsing it to `changed=false`.
- Create-only retry and rollback behavior is deterministic after each post-publication failure.
- Fault injection covers replacement, hardlink creation, temp removal, parent validation, sync, and close errors.

## Sub-Tasks

- [x] Inventory atomic-file outcome contracts and callers
- [x] Define the smallest typed publication result
- [x] Propagate post-publication state through privatefs consumers
- [x] Add failure-at-every-boundary and retry tests

## Notes

- Evidence: `internal/atomicfile/write.go`, `write_new.go`, and `write_stream.go` post-publication error paths.
- `PublicationResult` reports `PublicationNotPublished`, `PublicationPublishedUncertain`, or `PublicationDurablyPublished`, with an independent `Changed` bit for mode-only repairs.
- All atomic write, create-only, conditional, and stream APIs return the typed result. Parent-close failures downgrade a durable result to uncertain; post-publication sync and validation failures retain the published state.
- `privatefs.WritePrivateIfChanged` preserves the result through its opened-file security validation instead of returning `false` on an already-published artifact.
- Regression coverage injects parent synchronization failure after replacement, create-only linking, and stream replacement, and verifies the new bytes plus the uncertain outcome. Validation: `go test ./internal/atomicfile ./internal/privatefs -count=1`, `go test ./... -run '^$'`.

## Deviations

None.
