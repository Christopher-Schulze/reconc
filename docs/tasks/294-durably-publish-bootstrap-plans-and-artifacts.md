# TASK 294: Durably publish bootstrap plans and artifacts

## Why

Bootstrap syncs file contents but does not sync parent directories after initial plan creation, hardlink/copy publication, stage removal, or directory creation. A successful apply can therefore outlive process execution but not a filesystem crash.

## Acceptance

- Initial plan creation and every artifact publication sync the exact parent directory on supported Unix filesystems.
- Created directories and stage removal have an explicit ordered durability barrier.
- Windows uses the strongest documented supported equivalent without false fsync claims.
- Crash-injection tests cover create, link, copy fallback, cleanup, and nested directory creation.

## Sub-Tasks

- [ ] Define bootstrap directory-entry commit points
- [ ] Reuse or extend the existing rooted directory-sync primitive
- [ ] Add crash and dual sync-close failure tests
- [ ] Update bootstrap durability docs and run self-host gates

## Notes

- Evidence: `internal/bootstrap/plan.go:110-131` and `internal/bootstrap/transaction.go:481-659,860-892`.

## Deviations

None.
