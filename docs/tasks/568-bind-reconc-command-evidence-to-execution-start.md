# TASK 568: Bind Reconc command evidence to execution start

## Why

Finding F3: `reconc exec` resolves its active session and stamps the current
write epoch only after the command finishes. A parallel write can make an older
run appear fresh, and a session switch can assign it to an unrelated session.
The approved scope is the Reconc-owned execution boundary, whose start and end
are directly observable.

## Acceptance

- A command started before a relevant write retains its start epoch and cannot
  satisfy the later write's `require_command_success` gate.
- A session switch during execution never records the result in the replacement
  session; absence of an active session at start remains valid and cannot attach
  to a session created later.
- Ordinary successes and failures, real exit/signal values, taint handling, and
  staged candidate pre/postconditions retain their behavior.
- Deterministic synchronization tests exercise overlapping write/session changes
  with real session state and command execution rather than timing-only sleeps.
- Targeted session/CLI tests, the root suite, vet, lint, and development build pass;
  docs distinguish start-bound local evidence from exact staged proofs.
- The completed TASK is archived, committed, and pushed to origin/main.

## Sub-Tasks

- [ ] Design and reproduce the overlap/session-switch cases with existing session helpers.
- [ ] Capture the command's session and evidence epoch before execution and use that binding afterward.
- [ ] Propagate the internal API atomically, cover absence/taint/staged cases, and flush docs.
- [ ] Run validation, review all changes, archive, commit, and push.

## Technical Plan

Read every `RecordCommandOutcome` caller. Introduce a small concrete execution
binding in internal/runtime/agentsession containing the resolved repository,
selected session identity, and start evidence epoch. Capture it using existing
session locks and bounded state readers before `command.Run()`. Record outcomes
against that binding, never resolving a new active session or substituting a
later epoch. Reuse current AppendCommandResult, material-event signatures, and
overflow checks; reject a disappeared/corrupt bound session instead of recreating it.

Update internal/cli/exec_cmd.go and all internal API tests together. Use process
readiness/release markers or channels to order start, write/session switch, and
completion deterministically. Keep staged proof capture/verification independent.
Native host post-only event delivery is not changed into an invented process-start
claim by this TASK. No full-repository snapshot or new persistent journal is needed.

## Notes

Source owners: internal/cli/exec_cmd.go,
internal/runtime/agentsession/command.go, state.go, command_test.go.

## Deviations

None.
