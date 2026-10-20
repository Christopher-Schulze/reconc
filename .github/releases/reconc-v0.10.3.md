# reconc v0.10.3

Reconc 0.10.3 is a patch release that removes every way a governed session
could seal itself shut and gives the operator a sanctioned administration path
that never requires removing or stubbing hooks.

## Correction

These notes overstate several guarantees. Evidence-segment rotation, oversized
session state, and CRLF or concurrently written TASK boards could still lock a
session; the bounded command prefix dropped trailing command text from the
evidence that policy matched; `hook evidence-resolve` resets the resolved
session's evidence window; and the pre-tool guard refuses only recognized
operator-only invocations, so scripts, aliases, or copied binaries could still
run them. This release also shipped fail-open paths. The corrected behavior is
described in the [development notes](https://github.com/Christopher-Schulze/reconc/blob/main/.github/releases/development.md)
and ships with the next release.

## Fixes

- A command longer than 32 KiB no longer taints the session. Command and
  command-result evidence keep a UTF-8-safe prefix bound to the SHA-256 and
  byte length of the full text, so long heredocs stay ordinary work.
- `reconc hook claim` only persists the claim. It no longer re-runs the
  complete policy check, which executed every applicable `require_script`
  on the claim path.
- While evidence is tainted or the lockfile is stale, read-only diagnostics
  (`status`, `next`, `why`, `explain`, `doctor`, `sources`,
  `session-briefing`, `agent-intro`, `help`, `version`, `hook status`,
  `hook evidence-status`, `hook pause-status`, `run status`, `run log`,
  `task status`) still run from a vouched binary, so a blocked session can
  always explain itself.
- `reconc hook evidence-resolve` works during an active session. It clears
  that session's matching overflow under the session lock, so recovery never
  discards the agent's context.
- The stale-lockfile repair admission rejects file-writing redirections;
  descriptor duplication such as `2>&1` stays admitted.
- Deny matching skips Git global options, so a `forbid_command` rule for
  `git clean` also matches `git -C dir clean` and `git -c key=value clean`.
  Evidence matching stays literal.
- Codex shell calls reported as `exec_command` or `functions.exec_command` are
  gated as command tools instead of passing unchecked.
- Write-tool writes into the Claude Code session scratchpad of the current
  project are agent runtime state, not repository writes. Symlinks into the
  repository and other projects' scratchpads stay gated.

## Operator Administration

- `reconc hook pause [repo] --for DURATION --reason TEXT` lets blocking
  pre-tool, permission, MCP, Stop, subagent-Stop, and `reconc ci` gates pass
  with a notice for one minute to 24 hours, before any cached decision is
  consulted. Evidence keeps being recorded, and the destructive Git guard stays
  active. `reconc hook resume` ends it early; `reconc hook pause-status` and
  `reconc hook status` report it.
- Agent tool calls cannot issue operator-only commands: `hook pause`,
  `hook resume`, `hook evidence-resolve`, and `hook claim` for claims named
  `operator:*`. A `require_claim` rule that lists an `operator:` claim is
  therefore satisfiable only by the operator.

## Maintenance

- `usercli` tests use a private home, so an installed portable skill on the
  developer machine no longer breaks the lifecycle tests.
- Staticcheck is pinned to v0.8.2, which reads Go 1.27.2 export data.

## Compatibility And Upgrade

No policy-schema or lockfile-format change is introduced, and no manual data
migration is required. Existing immutable schema publication identities remain
unchanged. Policies keep working as written; adopting operator-only claims is
an explicit rename of a claim to the `operator:` prefix.

Use the installation's existing owner. For direct installations:

```bash
reconc update
reconc doctor --global
```

After updating the binary, use the documented repository synchronization
workflow to refresh generated integrations in existing repositories.

## Delivery

The tag-bound release workflow verifies immutable schema publication,
LangChain MCP interoperability, source and race-test gates, release trust, and
isolated self-hosting before building the platform artifacts. It verifies the
canonical manifests, checksums, and remote asset inventory before publication.
Published artifacts include build-provenance attestations. Windows artifacts
remain included; optional native Windows smoke is disabled by default.
