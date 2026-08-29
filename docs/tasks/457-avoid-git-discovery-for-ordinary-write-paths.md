# TASK 457: Avoid Git discovery for ordinary write paths

## Why

Write-hook memory filtering computes Claude project keys and may run `git rev-parse --git-common-dir` before checking whether any candidate path is absolute and inside the Claude memory tree. Ordinary relative repository writes pay this subprocess cost unnecessarily.

## Acceptance

- Relative and clearly non-memory paths return without Git discovery.
- One hook event computes Claude project identity at most once and only when an absolute candidate can reach the memory tree.
- Worktree/common-dir alias security and fail-closed symlink behavior remain unchanged.
- Benchmarks prove zero Git invocations on ordinary writes and lower latency while adversarial alias tests retain parity.

## Sub-Tasks

- [ ] Add cheap candidate preflight before project-key construction.
- [ ] Cache the exact matcher within one event/worker lifetime under safe root identity.
- [ ] Add invocation-count, worktree, alias, and symlink regressions.
- [ ] Run focused agent-memory tests and benchmarks.

## Notes

- Verified from finding 206 in `internal/runtime/agentsession/memory_paths.go`.

## Deviations
