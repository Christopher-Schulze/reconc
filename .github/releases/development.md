# Development notes

Unversioned development source. Tag creation and release publication require
separate explicit authorization. These notes describe the changes implemented
since the published `reconc-v0.11.0` release and do not establish a published
release.

## Installation and upgrade

- `reconc update` shows each phase (installation, release, skill, download,
  provenance, smoke test, publish) as it completes. On a terminal the running
  phase animates in place with its elapsed time and the download shows a byte
  progress bar; piped output stays plain. The summary states the version
  change, owner, and skill state, and lists only checks that need attention.
- `reconc doctor --global` no longer emits the unconditional
  `skill-discovery` warning, and update reports no longer repeat the
  pre-update comparison of the skill with the running binary.

## MCP gateway and assurance

- A client that sends an empty or `null` `_meta` with `tools/call` is no
  longer blocked as `protocol_error`.
- A downstream catalog refresh that changes no tool contract keeps the
  published generation, so in-flight calls, pending approvals, and progress
  streams stay valid.
- Drift found immediately before dispatch reports `policy_stale`,
  `tool_contract_stale`, `identity_unavailable`, or the cancellation instead of
  `state_unavailable`.
- Post-dispatch progress no longer re-hashes the downstream executable for
  every event; dispatch still verifies it.
- `applicable_if` globs skip `.git`, `.reconc`, and `node_modules` unless a
  pattern names them, so dependencies neither activate a gate nor exhaust the
  walk budget.
