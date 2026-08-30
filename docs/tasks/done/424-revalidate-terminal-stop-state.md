# TASK 424: Revalidate terminal Stop state

## Why

A clean Stop-cache hit returns before the TASK completion commit gate, a previous repeated-block hash survives a later clean evaluation, and uncacheable evaluation returns the Git snapshot captured before policy scripts ran. Terminal release can therefore use stale control-plane state.

## Acceptance

- Every clean Stop path executes the TASK completion gate.
- A successful clean evaluation clears obsolete repeated-block state.
- Terminal TASK and Git state are recaptured after evaluation before release, including uncacheable paths.
- Deterministic tests mutate TASK/Git state during evaluation and cover clean-cache, block-clean-block, and uncacheable flows.

## Sub-Tasks

- [x] Centralize terminal Stop release after cache and evaluation branches.
- [x] Reset repeat-block identity only after a verified clean result.
- [x] Recapture terminal TASK/Git state and fail closed on drift or inspection failure.
- [x] Run focused Stop-cache and repository-run tests.

## Notes

- Verified from findings 103, 104, and 201 in `stop_handler.go` and `stop_cache_core.go`.
- Clean report reuse now carries its revalidated TASK/Git snapshots into the same terminal finalizer used by evaluated reports; the finalizer recaptures both snapshots once more immediately before release.
- Any TASK or Git drift between the evaluated/reused report and terminal finalization exits fail closed. Pre-existing non-Git repositories retain their prior behavior unless the configured TASK commit gate requires trustworthy Git status.
- The terminal finalizer clears a prior policy-block identity only after a clean report and stable terminal snapshots, then runs the TASK completion commit gate on those current snapshots.
- Deterministic regressions cover a dirty terminal TASK on a clean cache hit, block-clean-block identity reset, and an uncacheable script that mutates the TASK control plane during evaluation.
- Focused terminal-state, Stop-cache, repeated-block, and repository-run tests passed in 1.015s, 4.052s, and 7.199s. Formatting and `git diff --check` passed.

## Deviations

- Full, race, vet, lint, static-analysis, release-trust, and platform gates remain deferred to the single queue-end verification run as requested.
