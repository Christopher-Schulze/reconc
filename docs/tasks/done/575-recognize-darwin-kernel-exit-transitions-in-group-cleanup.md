# TASK 575: Recognize Darwin kernel exit transitions in group cleanup

## Why

CI 37163182631 at c23aae43 exposed a remaining Darwin EPERM in script cleanup
and an unrelated Linux fixture error in the new signal test. The original
zombie-only classification misses the kernel exit transition: an exiting child
can retain P_stat=2 while P_WEXIT is set. The Linux test incorrectly uses its own
PID as a process-group ID, so the missing group produces ESRCH before EINVAL.

## Acceptance

- Real TERM-sensitive parent/child groups complete cleanup during Darwin's
  pre-zombie exit transition without an operational permission failure.
- EPERM remains an error whenever a native snapshot contains a process without
  either SZOMB or the kernel's irreversible P_WEXIT exit flag.
- Real zombie-only, live-group, resistant-descendant, timeout, and cancellation
  regressions remain valid; no retry delay or lifecycle weakening is added.
- Invalid-signal error preservation is tested against an actual existing Unix
  process group, independently of whether the test process leads that group.
- Focused non-race checks and a development build pass; docs are updated and the
  TASK is separately archived, committed, and pushed before central validation.

## Sub-Tasks

- [x] Reproduce the remaining native state and inspect kernel/SDK semantics.
- [x] Add a real group-exit regression and extend the existing classification narrowly.
- [x] Correct the Unix fixture's group identity and propagate documentation.
- [x] Run non-race validation, review, archive, commit, and push.

## Technical Plan

Retain the existing group signal and EPERM-only native snapshot. A member is
terminal when P_stat is SZOMB or its exported P_flag contains P_WEXIT (0x2000,
verified in the installed SDK). Every other member preserves EPERM. Do not add
retries, timers, per-PID signaling, a supervisor, or process-table injection.
Exercise actual Setpgid shell/sleep groups, wait for native membership, send
TERM, reap the parent, and call the public Signal cleanup path repeatedly.
Use Getpgrp for the invalid-signal test's existing group. Update only the current
signal contract in user docs and architecture. The final gate must cover the
new committed source, including remote macOS/Linux checks.

## Notes

CI: https://github.com/Christopher-Schulze/reconc/actions/runs/37163182631
Native diagnostic at c23aae43 observed ten EPERM snapshots with a non-zombie
child (P_stat=2, P_flag=0x6004) within 39 real parent/child cycles. An individual
signal-zero probe still returned success, so existence alone cannot classify it.
Apple's fill_user64_externproc exports P_LEXIT as P_WEXIT; exit_with_reason commits
that flag before task termination. proc_prepareexit moves the process out of the
live PID lookup before its state reaches SZOMB. The status/flag combination is a
real irreversible exit transition, not an inferred UID or timing exemption.
Sources: https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/kern_sysctl.c
and https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/kern_exit.c.
The prior central race run was stopped with its owned process group after the
new CI failure was confirmed; it is not claimed passed. Vet/staticcheck and
CodeQL passed at c23aae43, but do not substitute for final-source validation.
The new real-group regression failed on the previous classifier within 0.03 s.
Production changes are one verified SDK flag and one terminal-member condition.
No signal-zero existence inference or permission-error swallowing is introduced.
The fixed real-group regression passed five runs (500 native group lifecycles),
alongside live-group preservation and unsafe-ID/invalid-signal cases. Existing
script timeout/cancellation and MCP boundary regressions also pass uncached.
The full isolated-HOME make test-fast passed for both complete Go modules, and
make build passed. All changed code, docs, and the final diff were reread.
No per-TASK race or static run was started. Final central validation must bind
to the subsequent committed source, with macOS/Linux CI failures resolved.

## Deviations

None.
