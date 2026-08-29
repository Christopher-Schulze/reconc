# TASK 361: Publish CLI output without symlink truncation

## Why

Shared CLI output uses `os.Create`, which follows final symlinks and truncates the destination before rendering succeeds. A hostile output path can therefore overwrite another file, and a render failure destroys prior content.

## Acceptance

- File output rejects final symlinks and non-regular targets.
- Existing output remains intact until complete rendered bytes are ready for atomic publication.
- Standard output behavior and command-specific formatting remain unchanged.
- Tests cover symlink targets, render failure, existing files, and atomic replacement.

## Sub-Tasks

- [x] Replace eager `os.Create` output with safe buffered atomic publication.
- [x] Apply the shared behavior to every CLI caller.
- [x] Add symlink and failure-path regressions.
- [x] Run focused user CLI tests.

## Notes

- Verified from OMP session `01a04cc5-6a01-7526-95bb-b2715553daf3`, finding #107.
- Current evidence: `teeToFile` stages stdout bytes in a private temporary file and calls the shared atomic publisher only after rendering succeeds.
- Final symlinks and non-regular targets are rejected by the atomic publisher without changing the existing path; complete output replaces regular files atomically.
- All `teeToFile` callers preserve intentional non-zero command results after successful rendering while render and encoding errors abort publication.
- Focused verification passed: `go test ./internal/cli -run 'TestTeeToFile|TestProofJSONMarkdownAndAtomicOutput|TestGlobalDoctorWritesExactOutputFile|TestRun.*WritesOutputFile|TestTextCommandSurfacesOutputFailure' -count=1 -timeout=120s`.

## Deviations

- The repository-wide race, release-trust, and other heavy suites were not run, per the explicit execution constraint; local verification is limited to focused POSIX tests and short static gates.
- Windows-specific tests remain in the source and CI matrix but were not run locally, per the explicit execution constraint.
