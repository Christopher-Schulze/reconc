# TASK 513: Repair existing Windows integration gate failures

## Why

CI run 34579305219 at b5286a8f954cda0217415ff5d9852d5602fe22ba
already fails the full Windows suite before TASK 512. These failures are
independent of the product-version cleanup and prevent an overall green
Windows job and its later native installer step.

## Acceptance

- The native Windows full root and portable-template suites pass without
  disabling tests or weakening security and read-only guarantees.
- Generated adapter denial, read-only session inspection, supported special
  Git filenames, and native approval authority fixtures exercise real behavior.
- The same candidate reaches and passes Windows build, smoke, and installer
  steps; Linux and macOS regression gates remain green.

## Sub-Tasks

- [x] Reproduce and attribute each recorded Windows failure against current source.
- [x] Correct platform behavior or demonstrably invalid fixtures with focused tests.
- [~] Run native Windows and cross-platform gates, document, archive, commit, and push.

## Notes

- Source evidence: https://github.com/Christopher-Schulze/reconc/actions/runs/34579305219
  (`Windows build and smoke`, `Test full Windows suite`).
- `internal/cli/hook_scenario_e2e_test.go`: all eight
  `TestGeneratedAdaptersExecuteTemplateDenial` variants exit 1 without their
  expected denial envelope.
- `internal/cli/session_briefing_inspection_test.go`: four inspection tests
  observe changed `sessions/.../locks` directory modification times.
- `internal/runtime/git_test.go`: the special-name rename fixture attempts
  to create a tab-containing filename rejected by Windows.
- `internal/runtime/agentsession/native_approval_test.go`: four approval
  cases stop at `private Windows DACL is not protected` before their intended
  decision boundary.
- TASK 512 does not repair these paths. Do not infer a green Windows gate from
  local macOS validation or from passing version-identity tests.
- Investigation starts from clean commit 34f7770c940bab74e8bcd20015769fb5ed84be84.
  Its CI run 34584691581 already passes LangChain, macOS, and release trust;
  CodeQL run 34584690388 passes. Windows is still running its full suite.
- Inspect generated wrapper execution and environment first; distinguish
  failed process startup from the intended template denial. For session reads,
  trace lock-file lifecycle before changing any metadata assertion. Prepare
  native approval fixtures through the existing private-filesystem API so
  Windows DACL requirements remain enforced. Exercise legal Windows special
  filenames while retaining control-character coverage on supporting systems.

- Generated adapters now use the existing platform-aware shell resolver and
  report process-start errors explicitly. Native approval fixtures publish
  their registry through `privatefs` so the protected DACL is real. Windows
  rename fixtures retain Unicode, whitespace, and literal pathspec punctuation;
  POSIX fixtures retain tab coverage.
- Windows Go `DirEntry.Info` returns directory-enumeration metadata; the
  read-only inventory now obtains directory metadata through `File.Stat`.
  Microsoft documents that NTFS enumeration attributes can be stale and
  recommends handle-based information for current metadata:
  https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-findfirstfilea
  No membership, mode, size, timestamp, or content assertion was removed.
  Missing-report diagnostics are checked against the native filesystem error.
- Focused local CLI, Git rename, and native approval tests pass. Native Windows
  execution remains required; macOS results do not prove Windows completion.
- CI run 34584691581 on the unchanged starting commit reproduces exactly the
  same failures. Linux, macOS, LangChain, release trust, and CodeQL pass there.
  The native Windows log confirms the four fixture failure categories above.
- Local candidate verification passes: focused CLI/runtime/approval tests,
  `make test` (uncached root and portable-template race suites, publication
  audit, and release trust with a real artifact build), `make vet`, `make lint`,
  `make self-host`, and `git diff --check`. The implementation changes only
  test fixtures and their process-error reporting; production enforcement is
  unchanged. Native Windows acceptance remains pending candidate publication.

## Deviations

- Native Windows acceptance requires GitHub CI to receive the candidate on
  `main`; no Windows runner is available locally and branch creation is not
  authorized. Publish the locally verified implementation under the standing
  commit/push instruction while retaining this TASK as active, then archive
  only after native CI proves acceptance. This requires a separate completion
  record rather than falsely marking the TASK done before Windows execution.
