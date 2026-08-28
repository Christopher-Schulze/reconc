# TASK 336: Root bootstrap publication against path replacement

## Why

Bootstrap creates and inspects parent paths with absolute path operations before opening `os.Root` at the resulting directory. Ancestor or parent replacement between those steps can redirect publication before post-validation notices the swap; Windows directory identity is not pinned by an open handle.

## Acceptance

- One opened repository root owns component traversal, directory creation, publication, validation, and cleanup.
- Symlink, junction, ancestor, parent, and root replacement cannot cause any out-of-repository mutation.
- Created-directory rollback retains exact identity and ownership proof on Unix and Windows.
- Adversarial path-race, recovery, platform, and self-host tests pass.

## Sub-Tasks

- [ ] Map absolute-path operations and existing rooted primitives
- [ ] Move parent creation and publication under one repository root handle
- [ ] Pin Windows directory identity through cleanup
- [ ] Add ancestor and junction replacement tests

## Notes

- Evidence: `internal/bootstrap/transaction.go:860-958`, `publication_identity.go:13-35`, and Windows directory identity handling.

## Deviations

None.
