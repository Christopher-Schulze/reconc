# TASK 167: Serialize private JSONL lock publication

## Why

The first native Windows run for final commit `371af919` passed completely, but
the identical `main` run `31614358430` exposed a cross-process first-publication
race in the Action Ledger. One process could observe the newly created JSONL
lock after its directory entry existed but before the creating process applied
the protected owner-only DACL, then reject that legitimate transient state.

## Acceptance

- Exactly one process creates a missing JSONL lock with exclusive creation.
- The creator applies and verifies caller-owned filesystem security to a
  private candidate before atomically publishing the final lock path.
- Concurrent openers wait for first-publication security to finish before they
  validate or use the lock.
- Pre-existing symlink, special-file, identity, mode, or private-security drift
  still fails closed and is never repaired.
- Action Ledger read, append, recovery, and verification paths coordinate lock
  validation through the same cross-process boundary.
- Deterministic concurrency regression coverage fails under the old ordering
  and passes under the corrected ordering.
- Complete local gates pass and the exact committed source is ready for native
  Windows candidate and `main` verification before release replacement.
- Source and release version remain exactly `0.9.6`.

## Sub-Tasks

- [x] Make JSONL lock creation exclusive and security publication lock-ordered
- [x] Route Action Ledger existing-lock validation through that ordering
- [x] Add deterministic first-publication and unsafe-existing-lock regressions
- [x] Reconcile TASK, RFC, and product-documentation truth
- [x] Run complete local gates and prepare the exact source for remote proof

## Notes

The failure was `TestStoreSerializesMultipleProcesses`: one helper rejected
`ledger.lock` because its Windows DACL was not yet protected. The same SHA had
already passed this test in candidate run `31613415748`, proving ordering rather
than source drift.

The fix applies and validates the caller-owned private security contract on a
same-directory candidate, then publishes that exact file object under the final
lock path with no-replace hard-link semantics. Existing lock drift is never
repaired. Bounded directory-snapshot retries cover concurrent private-candidate
creation without accepting a persistent identity or metadata change.

Local proof passed: the focused multiprocess suite 100 times; root and portable
template race suites; publication and release-trust audits; vet; staticcheck;
complete root and template coverage measurement; all 50 discovered fuzz targets;
both module vulnerability scans; pinned Python 3.13.14 LangChain interoperability;
self-hosting; and the complete five-target `0.9.6` release build and verification.

## Deviations

None.
