# TASK 324: Stream audit archive verification

## Why

Audit loading reads every archive into a full byte slice and then `bytes.Split`s each file into another slice of line views before decoding. Maximum retained rings therefore carry avoidable peak memory and allocation cost.

## Acceptance

- Archive records are decoded incrementally with explicit per-line, per-file, and aggregate bounds.
- Final-newline, strict JSON, sequence, digest-chain, archive order, and exact line-number diagnostics remain unchanged.
- Snapshot callers still receive the complete bounded entry set they require, without a second full-file split structure.
- Maximum-ring allocation, corruption, audit, and race tests pass.

## Sub-Tasks

- [ ] Measure current maximum-ring peak allocation
- [ ] Add one bounded streaming line decoder
- [ ] Preserve exact source and line diagnostics
- [ ] Run audit, JSONL, retention, and benchmark gates

## Notes

- Evidence: `internal/audit/audit.go:666-691`.

## Deviations

None.
