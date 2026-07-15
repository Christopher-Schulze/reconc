# reconc v0.8.0

`v0.8.0` tightens evidence freshness, rejects misspelled policy fields, proves
hook execution per runtime route, and narrows the supported agent surface to
the integrations maintained by the project.

## Enforcement

- `require_command_success` records causal write epochs. A command run before a
  later matching write is stale; unrelated later writes do not invalidate it.
- Legacy unordered session evidence fails closed until the required command is
  rerun. Explicit CI command-success evidence remains authoritative for its
  complete evaluation snapshot.
- Unknown `.reconc.yml`, scope, rule, evidence, composite-check, and TASK
  lifecycle keys fail compilation. Free-form messages and prompts are unchanged.
- The public authoring contract is available as
  `schemas/v1/policy-config.schema.json`.

## Hooks

- GitHub Copilot generation, installation, scaffold, audit, runtime, and docs
  support is removed. Git, Claude Code, Codex, Cursor, OpenCode, Devin CLI,
  Antigravity CLI, and Kilo Code remain registry-owned.
- Hook status reports expected, observed, and unseen runtime routes instead of
  treating static configuration or one SessionStart event as execution proof.
- Per-route liveness writes are rate-limited to one update every six hours.
- Registry fail-open policy now governs handler failures as well as input-read
  failures, including non-blocking SessionStart routes.

## Release Artifacts

- `reconc-0.8.0-darwin-amd64`
- `reconc-0.8.0-darwin-arm64`
- `reconc-0.8.0-linux-amd64`
- `reconc-0.8.0-linux-arm64`
- `reconc-0.8.0-windows-amd64.exe`
- deterministic SPDX 2.3 and CycloneDX 1.6 SBOMs
- Bash, Zsh, and Fish completions
- man page
- four public v1 JSON schemas
- `SHA256SUMS`

## CI

- Direct pushes to `main`, contributor pull requests, and manual dispatch run
  the full cross-platform checks without making those checks a branch blocker.
- CI has read-only repository permissions and cannot create pull requests,
  issues, or auto-merges.
