# reconc v0.12.2

Reconc 0.12.2 lets an operator claim authorize exactly one change, makes every
claim revocable, keeps `reconc prune` working in crowded shared temporary
directories, and shows the transfer speed and remaining time while
`reconc update` downloads.

## Claims

- An `operator:` claim authorizes exactly one change. It stays effective only
  while Git `HEAD` is the commit that was current when it was recorded and only
  while the recording session remains the active session; a commit, amend,
  reset, checkout or rebase, a switch of the active session and the session's
  cleanup end it.
- `reconc hook claim <repo> <claim> --revoke` removes any claim, operator or
  agent, from the session's effective claims without confirmation; recording
  the claim again restores it. A revocation survives state reloads and sealed
  evidence segments.
- Pre-tool, MCP, Stop and completion decisions and the active evidence that
  `reconc ci` and `reconc impact` read use the effective claims, and a cached
  Stop decision is bound to the current grants.
- Operator claims recorded by an earlier release carry no grant and no longer
  authorize anything; the operator records them again for the next change that
  needs them.
- Grants and revocations live in one private, bounded per-project file that
  fails closed when it is damaged.

## Prune

- Shared temporary-root discovery reads directory entries in batches, scans at
  most 100,000 entries and keeps at most 16,384 Reconc-owned candidates.
  Unrelated names no longer consume the candidate budget, so `reconc prune`
  works in temporary directories that hold many foreign entries. A scan or
  budget error discards the whole temporary candidate inventory before any
  deletion.

## Update display

- On a terminal the `reconc update` download shows a smooth progress bar, the
  percentage, the transferred and total bytes, the smoothed transfer speed and
  the remaining time, and `stalled` while no bytes arrive for two seconds; the
  finished line keeps the size and the average speed. An update from 0.12.1
  runs the 0.12.1 binary, so the new display appears from the following update
  on.

## Maintenance

- The calibrated benchmark workflow runs only on manual dispatch.
- The reviewed host-contract baselines for Claude Code, Codex, Cursor, Devin
  CLI, DSH and Kimi Code follow their current official sources; no adapter
  change was needed. The live Codex probe stays qualified for codex-cli
  0.154.0.
