# TASK 012: Runloop control-plane hardening

## Why

Repository run mode must continue autonomously while executable TASK work exists, but yield immediately to an explicit interrupt or a normal user prompt. The fast Stop path also needs stronger semantic progress, terminal closure, periodic policy assurance, low-write hook liveness, an opt-in Go concurrency gate, and synchronized self-hosting hooks without restoring routine Git overhead.

## Acceptance

- Repository run mode blocks Stop while executable TASK work exists and disables on explicit interrupt or the next real user prompt without `/runloop`.
- Internal continuation prompts never disable repository run mode.
- Explicitly configured TASK planes fail closed when their overview is missing or unreadable.
- Optional committed-clean completion reuses the terminal Stop Git snapshot and performs no Git process on routine executable Stops.
- No-progress detection advances only on TASK or material tool progress, not reads or unrelated hook events.
- Long-running loops receive bounded policy checkpoints without full-policy work on every Stop.
- Hook liveness is observable with bounded, rate-limited state writes.
- The opt-in Go assurance pack detects changed production-code bare goroutines without affecting non-Go projects.
- Generated and installed self-hosting hooks are synchronized.
- Documentation, tests, race tests, vet, static analysis, build, and hook/runtime verification pass.

## Sub-Tasks

- [x] Tighten run-mode cancellation and semantic progress behavior.
- [x] Fail closed on configured TASK-plane drift and add committed-clean completion.
- [x] Add bounded policy checkpoints and low-write hook liveness.
- [x] Add the opt-in Go concurrency assurance gate.
- [x] Synchronize self-hosting hooks and bootstrap documentation.
- [x] Run full verification, archive the TASK, and commit the completed change.

## Notes

- GOLEM is a read-only proving ground. Only project-agnostic behavior is adapted here.
- Routine executable Stops must remain free of Git processes and full-policy reports.
- The Go concurrency gate is relevant only when a target project opts into the Go assurance pack; Reconc's own implementation language does not activate it elsewhere.
- Full unit/package test suite passed after implementation. The self-hosting Git pre-commit hook was regenerated from the current binary; every generated scaffold artifact already matched generator truth.
- Final verification passed: root and harness race suites, root and harness vet, pinned Staticcheck v0.7.0, release trust, self-hosting, schema parity, TASK validation, deep doctor, build, and all nine hook activation probes.
- Apple M1 benchmark at 100 iterations: routine repository Stop 0.545 ms/op versus 14.6-15.3 ms/op for clean full-policy paths, while routine Stops retain zero Git processes and zero full-policy reports.
- Final staged-policy review exposed three existing raw Git subprocess sites in touched files. They now use bounded `exec.CommandContext`; the generic Go process gate accepts context-owned commands without path-specific exemptions.

## Deviations
