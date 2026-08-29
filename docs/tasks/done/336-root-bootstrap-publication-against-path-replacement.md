# TASK 336: Root bootstrap publication against path replacement

## Why

Bootstrap creates and inspects parent paths with absolute path operations before opening `os.Root` at the resulting directory. Ancestor or parent replacement between those steps can redirect publication before post-validation notices the swap; Windows directory identity is not pinned by an open handle.

## Acceptance

- One opened repository root owns component traversal, directory creation, publication, validation, and cleanup.
- Symlink, junction, ancestor, parent, and root replacement cannot cause any out-of-repository mutation.
- Created-directory rollback retains exact identity and ownership proof on Unix and Windows.
- Adversarial path-race, recovery, platform, and self-host tests pass.

## Sub-Tasks

- [x] Map absolute-path operations and existing rooted primitives
- [x] Move parent creation and publication under one repository root handle
- [x] Pin Windows directory identity through cleanup
- [x] Add ancestor and junction replacement tests

## Notes

- Evidence: `internal/bootstrap/transaction.go:860-958`, `publication_identity.go:13-35`, and Windows directory identity handling.
- Bootstrap publication opens one checked repository `os.Root`, traverses and creates parents through nested roots, transfers the final parent handle to the created record, and retains parent roots for identity-safe rollback.
- Created-directory rollback uses the retained parent root and directory identity instead of reconstructing an absolute path, so an ancestor rename or replacement cannot redirect cleanup into a replacement repository.
- Regressions cover ancestor replacement during post-publication validation and rollback of nested parents after the repository path is replaced. Validation: `go test ./internal/bootstrap -count=1`, `GOOS=windows GOARCH=amd64 go test -c -o /dev/null ./internal/bootstrap`.

## Deviations

None.
