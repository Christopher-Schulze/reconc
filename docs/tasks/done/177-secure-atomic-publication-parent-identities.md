# TASK 177: Secure atomic publication parent identities

## Why

`atomicfile.WriteIfChanged` creates and uses the target parent with
`os.MkdirAll`, `os.CreateTemp`, and rename operations without proving that the
parent path is a stable non-symlink directory identity. A swapped or preexisting
symlink component can redirect a nominally repository-local publication.
Because this helper underpins lockfiles, journals, receipts, and reports, its
contract must be explicit instead of relying on every caller.

## Acceptance

- Publication proves every required parent component is a real directory and
  binds creation, temporary-file placement, replacement, and parent sync to the
  intended directory identity.
- Existing target comparison and mode reconciliation operate on stable regular
  file identities and never chmod a substituted symlink target.
- The API distinguishes intentionally permissive public parents from private
  parents instead of forcing `0755` creation for every caller.
- Linux/macOS and Windows tests cover parent symlink swaps, target swaps,
  missing parents, concurrent writers, replacement failure, and crash-safe
  cleanup without weakening atomicity.

## Sub-Tasks

- [x] Specify atomic publication directory and target invariants
- [x] Implement platform-specific identity-safe publication primitives
- [x] Migrate mode reconciliation and parent synchronization
- [x] Add adversarial race and portability tests
- [x] Audit every caller and run complete gates

## Notes

- Root evidence is `internal/atomicfile/write.go:16-88`.
- Do not solve this with string containment alone. Filesystem identity must be
  checked at the mutation boundary.
- The implementation will bind each absolute parent component through opened
  `os.Root` directory identities, retain those handles through publication,
  and revalidate the complete chain before target mutation and parent sync.
  Target comparison and mode repair will use an opened regular-file identity.
- Public-parent and private-parent creation remain explicit API choices:
  existing general outputs use `0755`; state-bearing callers opt into `0700`.
- `WriteIfChanged` and `WriteNew` now bind parent components with `os.Root`,
  use rooted rename/link/remove operations, and retain the opened directory
  roots until synchronization and final identity validation complete.
- Existing regular targets are compared through an opened file descriptor;
  mode repair calls `(*os.File).Chmod` on that descriptor and verifies the
  target identity afterward. Private state callers were migrated to the
  explicit private-parent API; compiler lockfile creation no longer performs
  an unbound `MkdirAll` first.
- Verification is green: the complete `make test` gate passed, including the
  race suite, harness template, publication audit, and release trust; the
  subsequent Windows-specific directory-sync hardening passed
  `go test -race ./internal/atomicfile` and
  `GOOS=windows GOARCH=amd64 go test -c ./internal/atomicfile`.

## Deviations

None.
