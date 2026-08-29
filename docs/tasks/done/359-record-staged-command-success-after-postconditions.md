# TASK 359: Record staged command success after postconditions

## Why

`reconc exec --staged` records a successful command outcome immediately after process exit, before checking that the command left the worktree clean. A zero-exit command that violates the staged postcondition can therefore produce success evidence although the CLI operation fails.

## Acceptance

- Staged command success is recorded only after every required staged postcondition passes.
- A zero-exit command that dirties forbidden state records a truthful failed or non-success outcome.
- Non-staged execution retains existing outcome semantics.
- Tests cover clean success, dirty zero-exit, command failure, and evidence-write failure.

## Sub-Tasks

- [x] Define the final outcome boundary for staged execution.
- [x] Reorder or classify evidence recording after postcondition verification.
- [x] Add CLI and runtime-evidence regressions.
- [x] Run focused exec integration tests.

## Notes

- Verified from OMP session `01a04cc5-6a01-7526-95bb-b2715553daf3`, finding #105.
- Current evidence: `internal/cli/exec_cmd.go` records process success before `VerifyStagedClean`.
- Staged execution now verifies the clean postcondition before recording a zero-exit success. A zero-exit command that dirties the candidate records a failure and cannot publish a success proof; command failures retain their real exit code and non-staged recording remains unchanged.
- Focused CLI exec tests cover clean success, index mutation, command failure, active-session evidence-write failure, and proof publication. `gofmt`, focused tests, `make vet`, `make lint`, and `git diff --check` pass. Repository-wide `make test-fast`, race, Windows execution, Release Trust, and other heavy gates remain intentionally unrun unless explicitly requested.

## Deviations

- The repository-wide race suite and heavy release gates are deferred per operator instruction; staged postcondition ordering is covered by deterministic CLI and active-session regressions, while CI retains cross-platform execution.
