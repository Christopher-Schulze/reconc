# TASK 314: Remove the discarded action-ledger checkpoint decode

## Why

On a cold append, the ledger reads and decodes the persisted checkpoint, discards both decoded state and error, and then performs the required full retained-chain rebuild. The I/O has no effect on correctness or recovery.

## Acceptance

- Cold append either uses a persisted checkpoint through a fully authenticated contract or performs the required full rebuild without a no-op read/decode.
- Checkpoint corruption, absence, external-writer changes, and historical tampering retain the TASK 271 fail-closed behavior.
- Startup and append benchmarks demonstrate the removed work without weakening verification.
- Ledger corruption, recovery, race, and benchmark gates pass.

## Sub-Tasks

- [ ] Reconfirm the checkpoint startup trust boundary
- [ ] Remove or meaningfully integrate the persisted decode
- [ ] Add corrupt and absent checkpoint regressions
- [ ] Run ledger scaling and complete gates

## Notes

- Evidence: `internal/actionledger/store.go:449-465`. TASK 271 requires full verification on startup unless an equivalent authenticated boundary is proven.

## Deviations

None.
