# TASK 424: Revalidate terminal Stop state

## Why

A clean Stop-cache hit returns before the TASK completion commit gate, a previous repeated-block hash survives a later clean evaluation, and uncacheable evaluation returns the Git snapshot captured before policy scripts ran. Terminal release can therefore use stale control-plane state.

## Acceptance

- Every clean Stop path executes the TASK completion gate.
- A successful clean evaluation clears obsolete repeated-block state.
- Terminal TASK and Git state are recaptured after evaluation before release, including uncacheable paths.
- Deterministic tests mutate TASK/Git state during evaluation and cover clean-cache, block-clean-block, and uncacheable flows.

## Sub-Tasks

- [ ] Centralize terminal Stop release after cache and evaluation branches.
- [ ] Reset repeat-block identity only after a verified clean result.
- [ ] Recapture terminal TASK/Git state and fail closed on drift or inspection failure.
- [ ] Run focused Stop-cache and repository-run tests.

## Notes

- Verified from findings 103, 104, and 201 in `stop_handler.go` and `stop_cache_core.go`.

## Deviations
