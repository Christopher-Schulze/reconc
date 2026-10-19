# TASK 577: Remove self-lockouts and add operator administration

## Why

A governed consumer repository had to disable every Reconc hook because the
product sealed its own session:

- A recorded command longer than 32 KiB (an ordinary large heredoc) set a
  durable evidence taint. PreToolUse then refused every Bash and write call,
  including Reconc's own diagnostics, and `hook evidence-resolve` refused while
  the session stayed active, so recovery required discarding the session.
- `hook claim` re-ran the complete policy check after persisting the claim and
  executed every applicable `require_script`, which hung for minutes.
- There is no sanctioned operator valve short of removing hooks, and every
  authorization claim can be recorded by the agent it is meant to restrain.

Further defects found while porting the consumer's local fixes:

- `forbid_command` prefix rules for Git miss global options (`git -C dir clean`).
- Codex shell calls reported as `exec_command` are not gated as commands.
- Write-tool writes into the Claude Code session scratchpad are refused as repo
  boundary escapes, although the host designates that directory for the agent.
- The stale-lockfile repair admission accepts `reconc refresh` with a
  redirection, which lets an unreviewed write through a sealed gate.

## Acceptance

- Commands and command results above the per-item limit are stored as a
  UTF-8-safe prefix bound to the SHA-256 and byte length of the full text; no
  evidence taint results, identical long commands deduplicate, and normalized
  state stays within every existing bound.
- `hook claim` persists the claim without running policy evaluation or scripts.
- While evidence is tainted or the lockfile is stale, read-only Reconc
  diagnostics (exact allowlist, no redirection, vouched binary) still run.
- `hook evidence-resolve` works during an active session and clears that
  session's overflow state atomically with the resolution record.
- `hook pause --for DURATION --reason TEXT` and `hook resume` exist; a pause is
  bounded (max 24h), expires on its own, is reported by `hook status`, and lets
  blocking hook events pass while evidence recording continues.
- Agent tool calls cannot issue operator-only commands: `hook pause`,
  `hook resume`, `hook evidence-resolve`, and `hook claim` for claims named
  `operator:*`. The guard also holds while paused.
- Git forbid rules match through global options; Codex `exec_command` is a
  command tool; Claude scratchpad writes for the same project are excluded from
  repo write policy; redirected repair commands are no longer admitted.
- Tests fail on the old behavior for every item; docs, generated references,
  skill references and release notes are current; `make test-fast`, `make vet`,
  `make lint` pass. Release notes belong to the separate authorized release preparation.

## Sub-Tasks

- [x] Bound overlong command evidence without tainting.
- [x] Make `hook claim` persist-only.
- [x] Admit read-only diagnostics under taint and stale lockfiles; reject file-writing redirected repairs.
- [x] Resolve evidence taint during an active session.
- [x] Add operator pause/resume and the operator-only command guard.
- [x] Match Git global options in command rules; gate Codex `exec_command`.
- [x] Exclude the Claude scratchpad from repo write policy.
- [x] Update docs, generated references and skill references.
- [x] Run the fast gate, vet and lint; review; commit and push.

## Technical Plan

Bounded command text lives in `internal/runtime/agentsession/state_limits.go`
and is applied where commands and command results enter state. The claim path
drops the post-claim `runCheckAndSave`. Diagnostics admission and the
operator guard sit next to the existing stale-lockfile repair admission and use
`shellcommand` invocation analysis plus `HasRedirect`. Pause state is a private
per-repository record beside the evidence taint record. Operator-only claims
use the `operator:` name prefix so no policy schema changes. Git global option
skipping is added to `matchInvocation` for Git targets with a literal
subcommand. Scratchpad exclusion reuses the Claude project-key matcher.

## Notes

- Authorization: Christopher explicitly requested these fixes, a commit and
  push on `main`, and the Reconc 0.10.3 release on 2026-10-19.
- Additional defects fixed while verifying: `usercli` lifecycle tests read the
  developer's real `~/.agents/skills/reconc` (package TestMain now gives every
  test a private home and pins Go's cache, module and env locations); the
  pinned Staticcheck v0.8.1 cannot read Go 1.27.2 export data, which would turn
  the next CI lint red (pin raised to v0.8.2 in Makefile, CI, release workflow
  and release-trust); `runCheckAndSave` became dead code after the claim change
  and was removed.
- The pause keeps the built-in destructive Git guard and the operator-only
  guard active; an operator pause replaces a damaged pause record instead of
  failing, while a damaged record never lifts enforcement.
- The 0.10.2 claim, repair-redirect and long-command tests that pinned the
  previous behavior were retargeted to the new contract; taint fixtures now use
  an oversized claim, which still cannot be retained.
- Verification: `make test` (publication audit, uncached root and template race
  suites, release trust) passed in 15m14s; `make test-fast`, `make vet`,
  `make lint` (Staticcheck v0.8.2), `make reference-docs-check`,
  `make fmt-check`, `make publication-audit`, `make harness-pack-check` and
  `make self-host` passed.
- Host-contract source watch: the scheduled maintenance job reports changed
  upstream hook documentation for 15 hosts (hash comparison only); the packaged
  fixtures are unchanged. It is separate from product CI and release
  eligibility and stays a maintenance review item.

## Deviations

None.
