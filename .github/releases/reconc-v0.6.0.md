# reconc-v0.6.0

This release moves Reconc from a policy checker into a cleaner repo-governance
toolkit for AI-assisted engineering. It includes the new `/runloop` continuation
surface, a simplified onboarding model, updated repo-local binary references,
and a stronger public README.

## Highlights

- Adds prompt-scoped `/runloop` autonomous continuation with session/runtime
  scoping, stop-file handling, no-progress guards, continuation decisions, and
  durable runloop state.
- Replaces the old degenmode naming across CLI, runtime packages, harness
  utilities, scaffold docs, tests, and agent-facing instructions.
- Removes `reconc setup` from the public CLI. Use `reconc bootstrap .` for the
  minimal init/compile/hook path.
- Clarifies the full rollout model: copy the Reconc toolkit into a target repo
  and have an agent follow `harness/template/BOOTSTRAP.md` for the full harness,
  `start.md`, TASK scaffold, hooks, and repo-local binary rollout.
- Refreshes README positioning with a visual RECONC hero, badges, clearer
  product explanation, rollout modes, and supported agent runtime table.

## Runtime And Hook Changes

- Keeps native hook coverage for Claude Code, Codex, Cursor, OpenCode, and
  Antigravity while routing generated runtime hooks through the repo-local
  `tools/reconc/bin/hook` wrapper.
- Updates generated git/OpenCode hook fallback paths to prefer
  `reconc-0.6.0-*` repo-local release binaries before PATH.
- Preserves git pre-commit as the hard repository backstop.
- Keeps stale `0.5.0` dist-path detection in the harness audit so older copied
  release binaries are surfaced during rollout validation.

## Workflow Changes

- `reconc bootstrap .` is now the documented human-facing onboarding command.
- `reconc setup` is intentionally gone and now fails as an unknown subcommand.
- `verify` and related docs now describe installation health instead of setup
  health.
- The full governance rollout is deliberately agent-driven via
  `harness/template/BOOTSTRAP.md`, not hidden inside a heavy CLI installer.

## Release Artifacts

The release workflow builds:

- `reconc-0.6.0-darwin-amd64`
- `reconc-0.6.0-darwin-arm64`
- `reconc-0.6.0-linux-amd64`
- `reconc-0.6.0-linux-arm64`
- `reconc-0.6.0-windows-amd64.exe`
- shell completions
- man page
- `SHA256SUMS`

## Upgrade Notes

- Replace any `reconc setup .` usage with `reconc bootstrap .`.
- Replace copied repo-local binaries named `reconc-0.5.0-*` with the matching
  `reconc-0.6.0-*` artifact.
- Use `/runloop` as the standalone prompt flag for autonomous continuation.
