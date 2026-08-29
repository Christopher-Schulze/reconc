# TASK 362: Preserve signal exit status in reconc exec

## Why

On Unix, a signaled child reports `ExitCode() == -1`, which the current mapping converts to generic exit code 1. This loses the conventional `128 + signal` status in both CLI behavior and command evidence.

## Acceptance

- Unix signal termination maps to the conventional `128 + signal` exit status.
- Recorded command evidence and the CLI process return the same derived status.
- Normal exit codes and non-exit execution errors retain existing behavior.
- Platform-specific tests cover representative signals and ordinary exits.

## Sub-Tasks

- [x] Add platform-aware exit-status extraction from process state.
- [x] Use one status for evidence and CLI return behavior.
- [x] Add Unix signal and cross-platform fallback regressions.
- [x] Run focused exec tests.

## Notes

- Verified from OMP session `01a04cc5-6a01-7526-95bb-b2715553daf3`, finding #108.
- Current evidence: Unix `syscall.WaitStatus` signal termination is mapped to `128 + signal`; normal process exits use `ProcessState.ExitCode`, and launch errors retain status 1.
- `runExec` computes the derived status once before recording `agentsession.CommandResult` and returning the `CLIError`, so both surfaces agree.
- Focused verification passed: `go test ./internal/cli -run 'TestExec(SignalExitStatusMatchesRecordedEvidence|PropagatesRealShellExitCode|StagedFailurePublishesNoProof|StagedProofSatisfiesCIWithoutToolHook)|TestCommandExitCode' -count=1 -timeout=120s`.

## Deviations

- Windows keeps the existing process-exit fallback and platform-specific regression source; Windows tests were not run locally, per the explicit execution constraint.
- The repository-wide race, release-trust, and other heavy suites were not run, per the explicit execution constraint.
