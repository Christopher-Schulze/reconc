# TASK 004: State retention and SSD hygiene

## Why

Session state is rewritten in full for every mutation, command evidence grows
without a hard bound, and existing pruning is harness-local and count-based.
Long sessions therefore create avoidable write amplification, stale state,
large reports, append-only decision logs, and temp/cache residue.

## Acceptance

- Unchanged session and active-session state is never rewritten.
- Repeated evidence is deduplicated and every persisted collection has a deterministic byte or item bound.
- Core pruning covers sessions, reports, locks, audit logs, runloop decisions, generated audit binaries, and abandoned temp files.
- Retention enforces per-class and total byte budgets, age limits, atomic cleanup, and safe active-file exclusions.
- Cheap lifecycle-triggered pruning works even when the harness audit cache never runs.
- Tests measure bounded growth, write counts, crash safety, concurrency, and cleanup correctness.

## Sub-Tasks

- [x] Define storage classes, byte budgets, age limits, and active-file invariants.
- [x] Add write-on-change state publication and bounded evidence compaction.
- [x] Move pruning into the product core and cover every runtime state class.
- [x] Wire cheap interval and lifecycle triggers without Stop-path friction.
- [x] Prove SSD-write, size, race, and crash-safety behavior.

## Notes

Approved areas: 10 Audit retention regression. The current `~/.reconc` sample
contains full-file rewrites and files above one megabyte, so count-only pruning
is not an acceptable bound.

Live audit evidence found 121 global state files (43 sessions, 37 reports, 34
locks), individual state/report files up to 1.6 MiB, a 76 MiB Golem `.reconc/`
tree, a 22 MiB runloop decision log, and abandoned `reconc-proof-*` temp trees
above 2 GiB combined. Product defaults: session/report classes each 32 files,
8 MiB and 14 days; live audit and runloop logs 2 MiB with two bounded archives;
generated harness binaries 32 MiB and 14 days; stale locks and build temps 24
hours; owned proof temp trees 24 hours; lifecycle prune interval 6 hours. Active
session/report/lock files, runloop state/locks, and live build-lock targets are
never pruning candidates.

Implementation proof: root `go test -count=1 ./...`, root
`go test -race -count=1 ./...`, `go vet ./...`, nested harness
`go test -count=1 ./...`, and Windows `GOOS=windows GOARCH=amd64 go build
./...` pass. Duplicate-evidence mutation is 88-99 us/op with zero state or
active-pointer publication; a not-due lifecycle check is 23-25 us/op and does
not scan the global temp tree. Concurrency tests cover session merge, bounded
JSONL append/rotation, one-winner lifecycle pruning, active-file exclusion,
dry-run immutability, legacy compaction, atomic temp cleanup, and fixed archive
rings. Dry-run paths create no retention locks, markers, directories, or temp
files. Legacy oversized JSONL files are compacted before they enter the archive
ring.

## Deviations

None.
