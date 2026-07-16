# reconc v0.8.2

`v0.8.2` adds first-class Grok Build enforcement and hardens same-session
continuation across the official ACP runtime and Grok leader mode. It also
verifies the downloaded release binary's provenance directly and refreshes the
project hero identity.

## Native Grok Integration

- `.grok/hooks/reconc.json` provides 14 generated native lifecycle, tool,
  permission, compaction, subagent, Stop, and session routes.
- Grok camelCase envelopes and native tool names are normalized into Reconc's
  canonical policy and evidence contract.
- Native PreToolUse emits Grok's exact allow/deny JSON. The generated wrapper
  converts ordinary Reconc runtime failures into an explicit deny instead of
  falling through Grok's fail-open error path.
- `reconc doctor --deep` verifies project trust and confirms that Grok loaded
  every generated native route.

## Strict Continuation

- `reconc grok` preflights hooks and trust, starts the unmodified official
  `grok agent stdio` ACP runtime, streams the response, and re-prompts the same
  session until the strict Stop gate is clean or the bounded continuation
  limit is reached.
- Leader-mode TUI sessions continue through `_x.ai/interject` without changing
  the Grok binary. Eligible leader Stops enable strict continuation before
  policy evaluation, so an unchanged blocking report cannot escape on the
  second Stop.
- The 32-attempt leader budget counts only one consecutive no-progress series
  for the same block. Material write or command progress, a new block, or a
  clean Stop resets it.
- User interrupts always win. ACP runner sessions disable leader steering so
  only one continuation driver is active.

## Cross-Platform Leader Transport

- Unix discovers `leader<suffix>.sock` endpoints under the Grok home and
  honours `GROK_LEADER_SOCKET`.
- Windows enumerates and dials Grok's `\\.\pipe\grok-leader-*` named pipes
  through Microsoft `go-winio`.
- Framed messages complete short writes, and multiple leader candidates receive
  fair shares of the bounded transport deadline.
- Deep doctor requires leader protocol version 1 and verifies
  `_x.ai/interject` with a random nonexistent session before reporting
  steering as active.

## Install And Documentation

- `install.sh` verifies the downloaded binary's GitHub build-provenance
  attestation directly when `gh` is available;
  `RECONC_REQUIRE_ATTESTATION=1` makes that proof mandatory.
- README, architecture, command, RFC, agent-guide, skill, bootstrap, and
  scaffold documentation describe the exact Grok enforcement boundary.
- The README hero now uses the simplified RECONC control-plane mark.

## Release Artifacts

- `reconc-0.8.2-darwin-amd64`
- `reconc-0.8.2-darwin-arm64`
- `reconc-0.8.2-linux-amd64`
- `reconc-0.8.2-linux-arm64`
- `reconc-0.8.2-windows-amd64.exe`
- deterministic SPDX 2.3 and CycloneDX 1.6 SBOMs
- Bash, Zsh, and Fish completions
- man page
- four public v1 JSON schemas
- `SHA256SUMS`
