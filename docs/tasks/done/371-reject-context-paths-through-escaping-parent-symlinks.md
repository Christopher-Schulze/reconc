# TASK 371: Reject context paths through escaping parent symlinks

## Why

Context-size inspection rejects a symlink only at the final path component. An intermediate directory symlink can lead outside the repository while the resolved leaf remains a regular file, violating the documented containment boundary.

## Acceptance

- Every context path is resolved beneath the repository root without following an escaping intermediate symlink.
- Final and intermediate symlinks follow one explicit, tested policy.
- Files reached outside the repository cannot contribute context evidence.
- Tests cover nested parent symlinks, in-root links if supported, replacement races, and ordinary files.

## Sub-Tasks

- [x] Define rooted context-path resolution semantics.
- [x] Implement component-wise containment and snapshot validation.
- [x] Add parent-symlink and replacement regressions.
- [x] Run focused context-size assurance tests.

## Notes

- Verified from OMP session `01a04cc5-6a01-7526-95bb-b2715553daf3`, finding #117.
- Current evidence: `internal/contextsize/context.go` uses `Lstat` on the complete path and does not validate intermediate components against the repository root.
- Context scans now anchor one `os.Root` for the repository. Intermediate directory symlinks are allowed only when the rooted operation resolves them inside that root; final symlinks remain rejected.
- Rooted open, pre/post identity checks, and a deterministic open-window hook prevent escaping parent replacement from contributing context metadata.
- Focused context-size and CLI tests, package vet, formatting, reference-doc checks, and `git diff --check` passed.

## Deviations

- Per explicit execution instruction, the full `make test`/race/release-trust gates and local Windows test execution were not run; the retained CI matrix and platform-specific tests were not removed or disabled.
