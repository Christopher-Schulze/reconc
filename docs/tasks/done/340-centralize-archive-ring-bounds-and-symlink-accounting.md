# TASK 340: Centralize archive-ring bounds and symlink accounting

## Why

Retention and diagnostic loops use duplicated numeric archive bounds, including a magic `32`, while some size probes use `os.Stat` and therefore count a symlink target's bytes before removing only the link. Reports and scan work can diverge from the actual JSONL ring contract.

## Acceptance

- Every archive consumer derives its bound from the owning JSONL or audit policy contract.
- Archive inspection uses non-following metadata and reports bytes actually owned and removable by the candidate path.
- Broken links, foreign links, oversized targets, sparse rings, and policy changes remain bounded and fail safely.
- Retention, doctor, JSONL, audit, and report tests pass.

## Sub-Tasks

- [x] Inventory archive constants and `Stat` probes
- [x] Expose one read-only ring-bound contract per owner
- [x] Correct symlink byte and cleanup accounting
- [x] Add sparse, linked, broken, and maximum-ring tests

## Notes

- Evidence includes `internal/retention/temp.go`, `retention/prune.go`, `internal/jsonl/enforce.go`, and `internal/cli/doctor_deep.go` archive loops.
- `jsonl.MaxArchiveFiles` and `jsonl.RingSize` now own the bounded archive contract. Ring inspection rejects links, special files, and archive indices outside the bound using non-following metadata. Generic repo-budget cleanup enumerates only the run-decision ring and preserves audit-chain archives.
- Verified with `go test ./internal/jsonl ./internal/retention ./internal/audit ./internal/cli -count=1`.

## Deviations

None.
