# reconc-v0.6.0

This release moves Reconc from a policy checker into a self-hosting repository
control plane for AI-assisted engineering. It includes transactional bootstrap,
typed TASK lifecycle, bounded runtime state, native assurance packs, nine hook
platforms, release trust verification, and the `/runloop` continuation surface.

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
- Adds deterministic `minimal`, `governed`, and `existing` bootstrap profiles.
  The mature-repository profile wires hooks, wrapper, and optional binary only,
  while preserving the existing policy, agent contract, docs, TASK state, and
  ignore policy.
- Adds a clean-repository self-hosting proof across all profiles, all nine hook
  platforms, policy health, TASK transitions, retention, and stable binary
  resolution.
- Expires abandoned proof worktrees and Go caches after two inactive hours,
  eliminating hard-kill residue while preserving recent work.
- Refreshes README positioning with a visual RECONC hero, badges, clearer
  product explanation, rollout modes, and supported agent runtime table.

## Runtime And Hook Changes

- Provides native hook coverage for Claude Code, Codex, Cursor, OpenCode,
  Devin CLI, Antigravity CLI, GitHub Copilot, and Kilo while routing generated
  runtime hooks through the repo-local `tools/reconc/bin/hook` wrapper.
- Makes hook binary discovery release-version agnostic: stable
  `reconc-<os>-<arch>` first, then exactly one compatible versioned artifact,
  development binaries, and PATH. Ambiguity fails closed.
- Preserves git pre-commit as the hard repository backstop.
- Keeps stale `0.5.0` dist-path detection in the harness audit so older copied
  release binaries are surfaced during rollout validation.

## Workflow Changes

- `reconc bootstrap .` is now the documented human-facing onboarding command.
- `reconc setup` is intentionally gone and now fails as an unknown subcommand.
- `verify` and related docs now describe installation health instead of setup
  health.
- Universal rollout is handled by reviewed `bootstrap inspect|plan|apply|verify`
  transactions. `harness/template/BOOTSTRAP.md` remains the AI tutorial and the
  manual path for project-specific harness, stack, architecture, and merge work.

## Release Artifacts

The release workflow builds:

- `reconc-0.6.0-darwin-amd64`
- `reconc-0.6.0-darwin-arm64`
- `reconc-0.6.0-linux-amd64`
- `reconc-0.6.0-linux-arm64`
- `reconc-0.6.0-windows-amd64.exe`
- shell completions
- man page
- three public JSON schemas
- `SHA256SUMS`

## Upgrade Notes

- Replace any `reconc setup .` usage with `reconc bootstrap .`.
- Replace copied repo-local binaries named `reconc-0.5.0-*` with the matching
  `reconc-0.6.0-*` artifact or install the stable platform name through a
  reviewed bootstrap plan.
- Re-run `reconc hook install <kind> --force` only for Reconc-managed hook
  artifacts that still hard-code a release number, then verify with
  `reconc hook status . --json`.
- Use `/runloop` as the standalone prompt flag for autonomous continuation.
