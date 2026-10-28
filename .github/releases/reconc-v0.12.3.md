# reconc v0.12.3

Reconc 0.12.3 names the Codex hooks that Codex will not run because their
`/hooks` approval is missing, stale or disabled, and keeps an expired operator
claim expired when Git `HEAD` returns to the commit it was recorded at.

## Codex hook approvals

- `reconc hook status` recomputes the definition hash that Codex approves under
  `/hooks` for every command handler of `.codex/hooks.json` and compares it
  with the `trusted_hash` entries in `$CODEX_HOME/config.toml` (default
  `~/.codex`). A Reconc hook that is disabled, never approved or approved for
  an older definition is named in the status detail together with the `/hooks`
  step. Codex stays `configured` in that case, because every regenerated
  definition starts unapproved and install and repository sync verification
  require that state; a project Codex does not trust reports `installed`.
- With every definition approved the status says so; without a Codex
  configuration or project entry, host trust stays unverified. An unreadable
  or invalid Codex configuration keeps `configured` with the error and a repair
  step. Reconc never writes Codex trust.
- `reconc doctor --deep` reports a configured platform that still has a host
  step left as `WARN`.

## Operator claims

- An operator grant binds Git `HEAD` and the size of `HEAD`'s reflog. A
  commit, amend, reset, checkout or rebase ends the claim for good, also when
  `HEAD` later returns to the granted commit, which previously made the grant
  effective again.
- Git failing to resolve `HEAD` is an error: recording fails and no grant is
  effective, so a grant recorded before the first commit no longer comes back
  when Git later cannot answer. Recording needs a `HEAD` reflog once `HEAD`
  names a commit (`core.logAllRefUpdates`).
- The Stop report cache binds the effective claims, so a decision taken while a
  claim was effective is not reused after the claim expired.
- Grants stored by 0.12.2 (`claim-grants-v1`) authorize nothing; their
  revocations stay in force. The operator records a claim again for the next
  change that needs it.

## Documentation

- The documentation, the command reference and the `reconc hook claim` help
  state that an operator claim authorizes the changes up to the next commit.
