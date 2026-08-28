# TASK 294: Durably publish bootstrap plans and artifacts

## Why

Bootstrap syncs file contents but does not sync parent directories after initial plan creation, hardlink/copy publication, stage removal, or directory creation. A successful apply can therefore outlive process execution but not a filesystem crash.

## Acceptance

- Initial plan creation and every artifact publication sync the exact parent directory on supported Unix filesystems.
- Created directories and stage removal have an explicit ordered durability barrier.
- Windows uses the strongest documented supported equivalent without false fsync claims.
- Crash-injection tests cover create, link, copy fallback, cleanup, and nested directory creation.

## Sub-Tasks

- [x] Define bootstrap directory-entry commit points
- [x] Reuse or extend the existing rooted directory-sync primitive
- [x] Add crash and dual sync-close failure tests
- [x] Update bootstrap durability docs and run self-host gates

## Notes

- Evidence: `internal/bootstrap/plan.go:110-131` and `internal/bootstrap/transaction.go:481-659,860-892`.
- Ordered Unix commit points are: sync each created directory's existing parent;
  sync staged payload, then its parent entry; sync the target parent after
  hard-link or exclusive-copy publication; sync it again after stage removal;
  and sync every rollback removal before reporting success. Windows retains
  payload `File.Sync` and the strongest supported create-only publication path
  without claiming a directory flush through a read-only `os.Root` handle.
- Verification passed: focused package tests, fault-injected barrier tests,
  focused Race, Windows amd64 cross-build and Vet, `make self-host`, `make vet`,
  `make lint`, and complete `make test` including uncached Race suites,
  publication/harness audits, and release trust.

## Deviations

None.
