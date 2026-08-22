# TASK 251: Harden filesystem, TASK, and installation transactions

## Why

Four current recovery and locking paths can leak or split authority under
failure. Atomic compare errors leak the opened current file until finalization,
TASK rollback discards safe-path errors, TASK inspection does not bind all
detail reads to one stable snapshot, and global installation purge unlinks a
lock file after releasing it. Explicit zero configuration also bypasses strict
TASK validation.

## Acceptance

- Every `atomicfile.writeIfChanged` path closes current and temporary
  descriptors exactly once and joins close errors with the primary failure.
- TASK rollback propagates every `safeTransactionPath` error before touching a
  path; symlink or ancestor replacement can never be reported as successful
  recovery.
- TASK inspection binds overview, referenced detail files, and pending
  transaction state to one before/after snapshot. Any changed, missing,
  replaced, or newly symlinked file produces `task/read/concurrent-mutation`.
- Explicit `done_visible: 0` and negative values fail validation, while an
  absent field retains the default. YAML presence is represented explicitly
  rather than inferred from the integer zero value.
- Installation purge never unlinks an inode used as the coordination lock.
  Concurrent update, uninstall, doctor, and install operations serialize on one
  stable lock identity before and after purge.
- Descriptor-leak tests observe repeated compare failures without growth;
  recovery and concurrency tests fail under the old implementations.
- Atomicfile, TASK lifecycle, user CLI, race, platform, and full repository
  gates pass, and transaction documentation matches the final lock ownership.

## Sub-Tasks

- [x] Close atomic current descriptors on compare errors with joined error reporting
- [x] Propagate safe-path failures through every TASK rollback branch
- [x] Snapshot and revalidate all TASK overview and detail reads
- [x] Preserve `done_visible` field presence and reject non-positive values
- [x] Keep one persistent installation lock inode across purge operations
- [x] Add descriptor, symlink-swap, concurrent-read, and inode-split regressions
- [x] Run focused race, platform, recovery, and full repository gates
- [x] Update TASK recovery and installation-lock documentation

## Notes

- External findings: F-60, F-81, F-92, F-93, and F-100.
- F-91 is excluded because Git completion inputs enumerate dirty files, not a
  bare unsuffixed grandparent directory. F-94 is already fixed by section-aware
  checklist parsing. F-95 conflicts with the globally unique TASK ID contract
  and current no-overwrite archive behavior.
- Atomic compare failures now transfer descriptor ownership to one close-and-
  join boundary; 512 forced truncation races leave the descriptor count flat.
- Both full and fast TASK readers bind every consumed file plus path-component
  identity and transaction absence to a final snapshot revalidation.
- Recovery no longer discards any rollback path error. Explicit zero/negative
  Done windows fail while omission remains the default of 10.
- Installation purge retains one private lock inode. Global diagnosis uses a
  non-mutating shared open and retries under that lock if installation begins
  during an initially absent-state observation.
- Evidence: focused package Race tests and Windows cross-compilation passed;
  `make test`, `make vet`, and `make lint` passed on 2026-08-22.

## Deviations

None.
