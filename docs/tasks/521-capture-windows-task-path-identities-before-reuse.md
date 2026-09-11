# TASK 521: Capture Windows TASK path identities before reuse

## Why

CI run 34618406388 passed Linux, macOS, release trust, and interoperability,
but Windows accepted a replaced TASK file in the existing path-guard regression.
Go's Windows Lstat defers file-ID lookup until SameFile; both deferred snapshots
can therefore resolve the replacement path during later revalidation.

## Acceptance

- Capture each Windows TASK component's identity before retaining its snapshot.
- Reject replacement even when file size, mode, and modification time match.
- Preserve Unix work, existing symlink rejection, and read-only inspection.
- Verify the focused regression and native Windows CI without redundant runs.

## Sub-Tasks

- [x] Trace the failing native test, guard callers, and actual Go file-ID behavior.
- [x] Capture identity at observation and make replacement metadata deterministic.
- [~] Verify focused/local and native gates, archive, commit, push, and resume TASK 520.

## Notes

- Failure: Windows job 103325974473, TestTaskPathGuardRejectsReplacementAfterRead,
  `replacement was accepted: <nil>`, at source 63b111fbc5ed583bafcf64a409fc6c1957748a95.
- Reuse the immediate two-observation SameFile pattern already used by
  `internal/bootstrap/directory_identity_windows.go`; apply it only on Windows
  before the guard retains FileInfo. No reliance on a self-comparison side effect.
- Keep the current benchmark run 34618412613 running; do not restart it for
  this separate task-state guard failure.
- Local tasklifecycle race suite passed in 5.066 seconds; scoped vet and
  staticcheck passed. The replacement test now fixes file and directory times
  so timestamp granularity cannot hide deferred identity lookup on Windows.

## Deviations

- Native Windows verification requires the implementation on a clean remote
  commit. Keep the task active until that CI evidence is available, then archive.
- Per the explicit efficiency instruction, do not duplicate the full suites
  locally before the required native CI; run the changed package locally and
  use the next source-bound CI for the complete platform and race gates.
