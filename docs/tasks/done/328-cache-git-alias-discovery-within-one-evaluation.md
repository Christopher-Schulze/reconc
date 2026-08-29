# TASK 328: Cache Git alias discovery within one evaluation

## Why

Shell guard handling can spawn `git config --get alias.<name>` repeatedly for unknown subcommands, while pre-decision identity separately enumerates all aliases. One hook event can issue redundant Git processes for the same immutable decision boundary.

## Acceptance

- One evaluation captures a bounded, hermetic alias snapshot and reuses it for identity and command analysis.
- Inline `-c alias.*` overrides retain precedence and dynamic aliases remain fail closed.
- Alias results are never reused across hook events or repository mutation boundaries without a new proven identity.
- Process-count, mutation, malicious-environment, shell, and race tests pass.

## Sub-Tasks

- [x] Map alias lookups and precedence rules
- [x] Introduce one request-scoped immutable alias snapshot
- [x] Rewire pre-decision identity and shell guard consumers
- [x] Add process-count and mutation regressions

## Notes

- Evidence: `internal/runtime/agentsession/shell_guard.go:226-305` and `pre_decision_cache.go:129-134`.
- `git config --null --get-regexp '^alias\\.'` emits bounded `key\\nvalue\\0` records; duplicate keys resolve last-write-wins, matching `git config --get`.
- The snapshot is captured once for the initial pre-decision identity, cloned before shell analysis, and recaptured for the post-evaluation identity. An incomplete snapshot is never authoritative and cannot suppress the existing fail-closed per-name lookup.
- Tests cover malformed output, repository-only aliases under hostile Git environment variables, inline precedence and dynamic aliases, immutable-map race safety, exact process count, and cache replacement after alias mutation.

## Deviations

None.
