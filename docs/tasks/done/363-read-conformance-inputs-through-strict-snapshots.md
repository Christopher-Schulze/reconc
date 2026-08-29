# TASK 363: Read conformance inputs through strict snapshots

## Why

Conformance input validation rejects a symlink via `Lstat`, then reads the path through a helper whose contract follows final symlinks. A replacement between those operations can substitute unvalidated bytes.

## Acceptance

- Conformance inputs are type-checked and read through one regular-file snapshot.
- Final symlink or file replacement between validation and reading fails closed.
- Size limits and existing diagnostics remain deterministic.
- Tests cover replacement and symlink substitution races.

## Sub-Tasks

- [x] Replace split `Lstat` and read operations with a bound snapshot.
- [x] Preserve bounded-read and diagnostic behavior.
- [x] Add adversarial conformance-file regressions.
- [x] Run focused conformance tests.

## Notes

- Verified from OMP session `01a04cc5-6a01-7526-95bb-b2715553daf3`, finding #109.
- Current evidence: the finding is already resolved in the current code. `readStrictConformanceFile` delegates to `boundedio.ReadRegularFile`, whose `ReadRegularFileSnapshot` opens one non-symlink regular-file snapshot, bounds the read, and validates opened/path identity, mode, size, and modification time after reading.
- The strict primitive was introduced by commit `dc3e30375a9c121de8b12b3fe8a95de121609bc4` (TASK 209), so the former `Lstat` plus symlink-following `ReadFile` behavior is no longer present on the active path.
- Existing adversarial coverage passed: `TestRegularFileSnapshotRejectsPathReplacement`, `TestOpenedSnapshotRejectsSameSizeMutation`, and `TestReadRegularFileRejectsSymlinkAndFIFO` in `internal/boundedio`.

## Deviations

- No product code changed because the reported split-read vulnerability is already closed by the shared strict snapshot primitive; changing the conformance caller would duplicate or weaken that existing boundary.
- The repository-wide race, release-trust, and other heavy suites were not run, per the explicit execution constraint; Windows-specific tests were not run locally.
