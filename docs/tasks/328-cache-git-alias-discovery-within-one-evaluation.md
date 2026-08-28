# TASK 328: Cache Git alias discovery within one evaluation

## Why

Shell guard handling can spawn `git config --get alias.<name>` repeatedly for unknown subcommands, while pre-decision identity separately enumerates all aliases. One hook event can issue redundant Git processes for the same immutable decision boundary.

## Acceptance

- One evaluation captures a bounded, hermetic alias snapshot and reuses it for identity and command analysis.
- Inline `-c alias.*` overrides retain precedence and dynamic aliases remain fail closed.
- Alias results are never reused across hook events or repository mutation boundaries without a new proven identity.
- Process-count, mutation, malicious-environment, shell, and race tests pass.

## Sub-Tasks

- [ ] Map alias lookups and precedence rules
- [ ] Introduce one request-scoped immutable alias snapshot
- [ ] Rewire pre-decision identity and shell guard consumers
- [ ] Add process-count and mutation regressions

## Notes

- Evidence: `internal/runtime/agentsession/shell_guard.go:226-305` and `pre_decision_cache.go:129-134`.

## Deviations

None.
