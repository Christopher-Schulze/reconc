# TASK 296: Sync repository-run state mutations before unlock

## Why

Repository-run state writes alternate CRC-protected slots with `WriteAt`, but normal save and mutation paths return and unlock without `file.Sync`. Only the reset/control path syncs, so the newest acknowledged state is not durably committed.

## Acceptance

- Every material repository-run mutation syncs before releasing its file lock.
- No-op mutations perform no unnecessary sync.
- Sync, unlock, and close errors are joined without masking the primary failure.
- Crash and fault-injection tests prove fallback to the prior valid slot and durability of an acknowledged newest slot.

## Sub-Tasks

- [x] Centralize the write-and-sync slot commit
- [x] Preserve no-op and alternating-slot behavior
- [x] Add sync, torn-write, and error-join tests
- [x] Run repository-run, Stop, race, and platform gates

## Notes

- Evidence: `internal/runtime/agentsession/repository_run_store.go:128-174` and callers in `repository_run.go:294-397`.
- The commit boundary is the alternating-slot `WriteAt` followed by `Sync`
  while `state.bin` remains locked. Unlock and close happen only after that
  commit returns, and no-op mutations bypass both operations.
- Verification: focused durability and torn-slot tests, the complete
  `internal/runtime/agentsession` package, focused race coverage, Windows amd64
  test compilation and vet, `make test`, `make vet`, `make lint`, and
  `make self-host` all passed.

## Deviations

None.
