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

## Hook performance

- A hook request resolves each recorded path once and only revalidates it
  afterwards: the read-path filter and every policy check share one path
  resolver, confirmed-missing directories are not walked again, existing files
  take their identity from their revalidated directory, and macOS reads a
  directory listing instead of opening each file for its stored spelling. A
  pre-tool call in a Claude Code session with 1,000 recorded reads, 200 writes,
  and 200 commands took 18 ms instead of 85 ms on an Apple M1.
- A pre-tool call no longer rewrites an unchanged session state or takes the
  active-session lock when the pointer is current, post-tool handlers decode
  the hook payload once, size-checked files are read with one exact
  allocation, and the command-proof capture repairs its directory once.

## Stop

- Stop builds its cache fingerprint (Git index entries, dirty-file hashes,
  evidence serialization) and the report and evidence hashes only for a plan
  it can cache, and looks a cached entry up before hashing the evidence. A Stop
  under a `require_script` without `cache_inputs` and 64 dirty files took
  31 ms instead of 70 ms.
- A clean Stop no longer locks and rewrites the session only to clear a
  repeated-block marker that is not set, and the dirty-path scope resolves the
  repository root once.
- A Stop blocked because the repository changed during evaluation names the
  changed paths, so a check that writes into the repository can be found.
