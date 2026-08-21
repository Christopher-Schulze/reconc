# TASK 226: Derive action-ledger archives from policy

## Why

The Action Plane ledger declares `MaxArchives`, and rotation, query ordering,
rendering, and archive counting generally use it. Directory allowlisting and
archive-gap validation still encode the current value `2` as the literal names
`ledger.jsonl.1`, `ledger.jsonl.2`, transaction-backup indices `0`, `1`, `2`,
and direct `exists[1]`/`exists[2]` checks. A future retention change could then
make recovery reject valid members, accept an incomplete archive ring, or index
outside the validation slice. Every ledger member and contiguity invariant must
derive from the same policy value.

## Acceptance

- Stable ledger inventory accepts the live file and archive suffixes `1`
  through `MaxArchives`, with no literal archive-number allowlist.
- Transaction recovery accepts backup indices `0` through `MaxArchives`, which
  correspond to the live file plus every retained archive.
- Numeric suffix parsing rejects empty, signed, non-decimal, leading-zero,
  overflowed, negative, and out-of-range values without allocating an
  unbounded intermediate.
- Archive validation rejects every gap: an archive requires the live file and
  archive `n` requires every archive `1..n-1`.
- The logic remains correct for policy values 0, 1, 2, and values greater than
  2 without editing production constants during a test run.
- Rotation order, oldest-first query order, detached-head verification,
  active-call retention protection, journal recovery, private modes, and
  unexpected-member rejection remain unchanged.
- Table-driven tests cover complete rings, every single gap position, orphaned
  archives, upper-bound members, out-of-range members, transaction state, and
  recovery cleanup; fuzz coverage exercises suffix parsing and archive sets.
- Existing ledger schemas and on-disk filenames remain compatible.

## Sub-Tasks

- [x] Define policy-derived archive and transaction-backup name helpers
- [x] Replace literal inventory allowlists with strict bounded parsing
- [x] Generalize archive-contiguity validation for the full configured range
- [x] Add parameterized, corruption, recovery, and fuzz regressions
- [x] Run action-ledger, JSONL, race, and publication verification

## Notes

- Session findings: `#23` and `#23a`.
- Primary code: `internal/actionledger/store.go`,
  `internal/actionledger/verify.go`, `internal/actionledger/types.go`, and
  `internal/jsonl/jsonl.go`.
- `MaxArchives` is currently 2. Tests should exercise the invariant through a
  pure helper that accepts a maximum, not through mutable global production
  policy.
- Do not broaden the accepted action-directory inventory. Files unrelated to
  the declared live/archive/journal transaction remain corruption evidence.
- Verification: actionledger and JSONL package tests, race tests, and vet passed.

## Deviations

None.
