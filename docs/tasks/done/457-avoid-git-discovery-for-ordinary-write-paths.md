# TASK 457: Avoid Git discovery for ordinary write paths

## Why

Write-hook memory filtering computes Claude project keys and may run `git rev-parse --git-common-dir` before checking whether any candidate path is absolute and inside the Claude memory tree. Ordinary relative repository writes pay this subprocess cost unnecessarily.

## Acceptance

- Relative and clearly non-memory paths return without Git discovery.
- One hook event computes Claude project identity at most once and only when an absolute candidate can reach the memory tree.
- Worktree/common-dir alias security and fail-closed symlink behavior remain unchanged.
- Benchmarks prove zero Git invocations on ordinary writes and lower latency while adversarial alias tests retain parity.

## Sub-Tasks

- [x] Add cheap candidate preflight before project-key construction.
- [x] Cache the exact matcher within one event/worker lifetime under safe root identity.
- [x] Add invocation-count, worktree, alias, and symlink regressions.
- [x] Run focused agent-memory tests and benchmarks.

## Notes

- Verified from finding 206 in `internal/runtime/agentsession/memory_paths.go`.
- Filesystem identity now qualifies each absolute candidate before the exact
  project-key matcher is loaded; relative and resolved non-memory paths never
  reach Git common-directory discovery.
- One filtering event lazily loads one matcher. Worktree/common-dir aliases,
  unrelated projects, and a symlinked memory escape retain the prior decision.
- Focused agent-memory tests passed with `-count=1`.
- Apple M1, 20 iterations: preflight `56,906 ns/op`, `9,960 B/op`, `104
  allocs/op`, `0 git-invocations/op`; eager baseline `12,064,552 ns/op`,
  `115,168 B/op`, `439 allocs/op`, `1 git-invocation/op`.

## Deviations
