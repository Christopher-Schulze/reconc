# TASK 280: Harden TASK lifecycle transactions

## Why

TASK transaction admission uses `Lstat` in the standalone pending check but
`Stat` before apply, so a symlink/dangling-symlink journal is interpreted
inconsistently. Move publication creates destination parent directories that
are absent from the journal and remain after rollback. Lock-file close and
unlock errors are discarded. Done-window trimming assumes rows are already
newest-first although validation currently enforces only the count.

## Acceptance

- Every pending-journal check uses one non-symlink regular-file contract and
  rejects symlink, dangling symlink, directory, oversized, replaced, and
  permission-error states consistently.
- Transaction move planning records every destination parent directory that
  will be created, with identity/ownership evidence sufficient for safe reverse
  cleanup.
- Rollback removes only empty directories created by the transaction, deepest
  first, after validating their identity. User-created, replaced, non-empty, or
  pre-existing directories are preserved and reported.
- Lock open, acquisition, unlock, and close errors are joined into the returned
  transaction result. A successful mutation is not reported as fully durable
  if final lock/journal cleanup fails.
- The board parser validates Done rows in descending numeric TASK order before
  trimming, or trimming selects the newest IDs independent of input order and
  restores canonical rendering. No newer row can be silently removed.
- Recovery handles journals produced before this change or rejects them with
  explicit compatible remediation; no format change is implicit.
- Crash-injection tests cover every move boundary, parent creation, rollback,
  journal replacement, lock cleanup, and out-of-order Done rows on all
  supported path syntaxes.
- TASK CLI docs, self-host tests, race tests, and complete gates pass.

## Sub-Tasks

- [ ] Unify pending-journal type, identity, and size admission
- [ ] Extend transaction planning with created-directory rollback records
- [ ] Implement identity-checked deepest-first rollback cleanup
- [ ] Join lock, unlock, close, and journal-cleanup errors
- [ ] Enforce or normalize newest-first Done ordering before window trimming
- [ ] Define compatibility behavior for existing transaction journals
- [ ] Add crash, replacement, parent, ordering, and cleanup regression tests
- [ ] Update TASK transaction/recovery documentation
- [ ] Run task lifecycle, self-host, race, and complete repository verification

## Notes

- Current evidence: `transactionExists` uses `os.Lstat`, while
  `applyTransaction` checks `journalPath` with `os.Stat`.
- Current evidence: `publishTransactionMove` calls `os.MkdirAll` for the
  destination parent, but `transaction` contains no created-directory records.
- Current evidence: `withMutationLock` uses bare `defer file.Close()` and
  `defer unlock()`.
- Current evidence: `trimDoneRows` retains the first `doneVisible` rendered rows;
  parser validation reports excessive count but not descending task ID order.
- This task must preserve the repository's Main-only workflow rule and must not
  create a branch during implementation.

## Deviations

None.
