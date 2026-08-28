# TASK 314: Remove the discarded action-ledger checkpoint decode

## Why

On a cold append, the ledger reads and decodes the persisted checkpoint, discards both decoded state and error, and then performs the required full retained-chain rebuild. The I/O has no effect on correctness or recovery.

## Acceptance

- Cold append either uses a persisted checkpoint through a fully authenticated contract or performs the required full rebuild without a no-op read/decode.
- Checkpoint corruption, absence, external-writer changes, and historical tampering retain the TASK 271 fail-closed behavior.
- Startup and append benchmarks demonstrate the removed work without weakening verification.
- Ledger corruption, recovery, race, and benchmark gates pass.

## Sub-Tasks

- [x] Reconfirm the checkpoint startup trust boundary
- [x] Remove or meaningfully integrate the persisted decode
- [x] Add corrupt and absent checkpoint regressions
- [x] Run ledger scaling and complete gates

## Notes

- Evidence: `internal/actionledger/store.go:449-465`. TASK 271 requires full verification on startup unless an equivalent authenticated boundary is proven.
- The persisted checkpoint cannot independently restore terminal-call membership. A cold store must therefore verify the complete retained chain and rebuild the in-memory membership index; decoding and discarding the persisted payload proves nothing additional.
- Apple M1 pre-change cold-load samples with 256 terminal calls at 20 fixed iterations: 34,867,967 to 38,231,056 ns/op, 37,845,212 to 37,970,226 B/op, and 35,133 to 36,175 allocs/op.
- Apple M1 post-change median at the same three-by-20 calibration was 34,450,850 ns/op and 37,755,739 B/op, versus 35,187,475 ns/op and 37,923,782 B/op before removal. Allocation samples were noisy and showed no supported improvement claim.
- Verification: focused checkpoint tests, action-ledger race tests, four five-second fuzz targets, cold-load benchmark, complete `make test`, `make vet`, Staticcheck v0.8.1, and `make self-host` all passed.

## Deviations

None.
