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
- Verify the focused regression without redundant runs.
- Preserve Windows code and tests; replace automatic native gates with manual
  smoke jobs capped at two minutes, without blocking main or releases.

## Sub-Tasks

- [x] Trace the failing native test, guard callers, and actual Go file-ID behavior.
- [x] Capture identity at observation and make replacement metadata deterministic.
- [x] Apply the requested Windows validation limit, verify policy, archive,
  commit, push, and resume TASK 520.

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
- Both workflows now expose only optional Windows smoke with a two-minute
  whole-job limit; the short filesystem selection has a 90-second per-binary
  timeout. Windows code, installer tests, and release artifacts remain intact.
- Read back ruleset 18998289 after removing only `Windows build and smoke`.
  All five other required checks, bypass actors, and protections are unchanged.
- Focused YAML/workflow and LangChain prerequisite tests passed. The complete
  release-trust workflow-policy prefix, reference check, Bash syntax, and diff
  check passed. ShellCheck reports only the same three pre-existing warnings
  around the intentional fixture `RELEASE_TAG` assignment, with no new findings.
- CI 34620481438 was cancelled as requested; CodeQL 34620481464 passed.
  Complete Linux/macOS CI will run on the policy commit without native Windows.

## Deviations

- Christopher explicitly ended extended native Windows validation on September
  11. Cancelled CI run 34620481438; no new native Windows run is required for
  completion. Local regression evidence remains valid; the Windows-specific
  identity fix has no completed native verification. Preserve all test definitions.
- Per the explicit efficiency instruction, do not duplicate the full suites
  locally; run changed contracts locally and use source-bound Linux/macOS CI
  for complete platform and race gates. Native Windows is no longer required.
