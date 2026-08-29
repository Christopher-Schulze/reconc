# TASK 324: Stream audit archive verification

## Why

Audit loading reads every archive into a full byte slice and then `bytes.Split`s each file into another slice of line views before decoding. Maximum retained rings therefore carry avoidable peak memory and allocation cost.

## Acceptance

- Archive records are decoded incrementally with explicit per-line, per-file, and aggregate bounds.
- Final-newline, strict JSON, sequence, digest-chain, archive order, and exact line-number diagnostics remain unchanged.
- Snapshot callers still receive the complete bounded entry set they require, without a second full-file split structure.
- Maximum-ring allocation, corruption, audit, and race tests pass.

## Sub-Tasks

- [x] Measure current maximum-ring peak allocation
- [x] Add one bounded streaming line decoder
- [x] Preserve exact source and line diagnostics
- [x] Run audit, JSONL, retention, and benchmark gates

## Notes

- Evidence: `internal/audit/audit.go:666-691`.
- `readAuditEntries` now reads each bounded regular-file snapshot through one
  `bufio.Reader`, enforcing 32 KiB records, 2 MiB files, and a 6 MiB retained
  ring while preserving archive order and line diagnostics.
- `BenchmarkAuditReadMaximumRing` measures the complete retained ring; the
  one-iteration measurement is 131.7 ms, 443,043,680 B/op, and 1,258,711
  allocations/op on Apple M1.
- Verified with targeted malformed/oversized/truncated tests, `go test -race
  ./internal/audit ./internal/jsonl`, `make test-fast`, `make vet`, `make
  lint`, and `make self-host`.

## Deviations

None.
