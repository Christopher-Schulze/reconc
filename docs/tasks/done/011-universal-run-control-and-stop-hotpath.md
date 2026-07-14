# TASK 011: Universal run control and Stop hotpath

## Why

Autonomous TASK continuation is currently split between a prompt-scoped
Runloop and a Golem-only Devin repo loop. Reconc needs one explicit,
project-agnostic control surface that every supported agent runtime can use,
while routine continuation avoids redundant policy, Git, state, and log work.

## Acceptance

- `reconc run on|off|status|log` is the canonical AI-operated control surface, with backward-compatible `runloop` observability and prompt activation.
- Repo-scoped mode works through all eight registered agent platforms and session-scoped mode preserves existing `/runloop` behavior.
- Both `sections-v1` and `logbook-v1` TASK profiles drive continuation, repair, blocker, promotion, and terminal-release decisions through the typed lifecycle package.
- Routine continuation does not run full Stop policy or spawn Git processes; terminal release still requires policy and TASK closure truth.
- Disabled and unchanged hook events do not rewrite state, create stop markers, or append meaningless decision records.
- Bootstrap, agent guidance, help, completion, self-hosting, release metadata, and generated adapters document and verify AI-owned operation.
- Benchmarks quantify handler, wrapper, Git, state, log, and end-to-end gains without weakening pre-write, TASK mutation, pre-commit, or terminal Stop gates.

## Sub-Tasks

- [x] Lock the universal run-control state, TASK decision, and CLI contracts.
- [x] Implement `reconc run on|off|status|log` and compatibility behavior.
- [x] Optimize Stop state transitions, Git usage, decision persistence, and wrapper resolution.
- [x] Prove all agent adapters, both TASK profiles, races, no-progress, blockers, and terminal release.
- [x] Propagate AI guidance, bootstrap, completion, self-hosting, architecture, and release documentation.
- [x] Run full gates and before/after benchmarks, archive the TASK, and commit one verified change.

## Notes

Approved design: universal repo-scoped switch, session-scoped compatibility,
typed TASK coupling, early continuation fastpath, direct filesystem progress
signals, write-on-change state, transition-only logs, and AI-owned commands.
Golem remains read-only evidence throughout implementation.

Apple M1 / Go 1.26 benchmark, five runs of 100 Stop iterations, median:
pre-TASK-011 Runloop Stop `13.469407 ms/op`, `191570 B/op`, `1390 allocs/op`;
repository Stop hotpath `0.606288 ms/op`, `68659 B/op`, `623 allocs/op`.
That is 95.50% lower latency (22.22x), 64.16% fewer allocated bytes, and
55.18% fewer allocations. Five 500-exec wrapper runs measured a median
`9.88 ms/exec` before and `6.28 ms/exec` after, 36.44% lower (1.57x).
Routine executable Stop also changes Git subprocesses from one to zero,
full policy reports from one to zero, and empty session publications from one
to zero on a session's first routine Stop. Disabled/unchanged run events
change state/marker/decision writes to zero; terminal and pre-write gates
remain intact.

The final fresh-eyes pass caught two real boundary defects before completion:
queued TASKs with unfinished dependencies could be reported as claimable, and
corrupt run state could be replaced during mutation. Typed dependency
selection now reports the queue as blocked, and every run-state mutation fails
closed without replacing corrupt bytes.

Final proof passed on the exact end state: formatting, module tidy checks,
product and nested-harness tests, full race suites, Vet, pinned Staticcheck
v0.7.0, self-hosting golden path, release-trust negatives, five-target release
build, completions, man page, schemas, checksums, and strict artifact
verification. Generated root and toolkit wrappers are byte-identical, all
eight native agent Stop shapes continue repository mode, and `git diff
--check` is clean.

## Deviations

None.
