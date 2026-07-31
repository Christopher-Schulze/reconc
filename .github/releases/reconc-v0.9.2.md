# reconc v0.9.2

Reconc v0.9.2 is a compatibility-preserving patch release that hardens the
public CLI, repository-owned transactions, agent integrations, and release
truth built on v0.9.1. It does not introduce a policy or schema migration.

## Added

- Kimi Code CLI support through an explicit user-global 16-event TOML hook
  integration. The managed block preserves unrelated configuration, discovers
  an explicitly configured Reconc repository from the invocation directory,
  and otherwise no-ops.
- Registry-derived Cursor CLI surface events and exact live-route reporting,
  including the official `agent` executable and the `cursor-agent`
  compatibility alias.
- `reconc sources` for body-free effective source provenance.
- Nested `reconc help` paths backed by the same authoritative command metadata
  used for dispatch, completions, the man page, and command documentation.
- Checksum-bound `reconc repo sync resolve` strategies and
  `reconc repo sync recover` for reviewed drift and interrupted transactions.
- Durable, fail-closed TASK transaction recovery with exact before-image,
  destination, journal, and mode validation.

## Improved

- Repository sync now plans from one hermetic Git snapshot, binds the complete
  plan to its digest and preconditions, writes a durable before/after journal
  before mutation, and verifies the full result under the repository lock.
- Repository sync resolution publishes an ownership receipt, requires a fresh
  plan, and never treats receipt or journal deletion as remediation.
- Cursor integration separates documented surface events, artifact loading,
  exact-route observation, negative enforcement proof, and unsupported host
  behavior instead of promoting static configuration into live proof.
- Kimi Code installation is deliberately excluded from `init`, bootstrap, and
  repository scaffold sync because the host configuration is user-global.
- CLI operands, help, completion, man-page, and documentation contracts now
  derive from one command catalog and reject ambiguous or extra operands.
- Policy compilation, migrations, source ingestion, audit JSONL, lock diffs,
  templates, retention, and release discovery use stricter bounds and
  deterministic identities.
- Semantic-version ordering handles arbitrarily large numeric prerelease
  identifiers without integer overflow and uses POSIX `C` collation in the
  native installer.

## Fixed

- Removed the non-production `reconc demo` surface and its private fixture
  engine. The project video remains the demonstration path.
- Removed misleading legacy and quality command surfaces that duplicated
  canonical commands or implied guarantees they did not provide.
- TASK mutations now publish all related files as one no-clobber transaction,
  retain exact file modes, reject symlink and destination drift, and recover
  only from a valid strict journal.
- Repository sync no longer evaluates a moving worktree between policy,
  ownership, and publication checks.
- Cursor `postToolUseFailure` is failure evidence only, while
  `afterShellExecution` remains passive because the host supplies no
  authoritative exit status.
- Kimi Code crashes, timeouts, non-2 failures, and post-tool payloads without
  an authoritative exit status can no longer be represented as enforced
  success.
- Release inventory, schema-lock integrity, safe names, case-aware path
  identity, and retained audit data reject malformed, ambiguous, or
  unbounded inputs instead of normalizing them into a pass.
- The exhaustive publication audit retains a hard deadline but now has enough
  headroom for race-instrumented and resource-constrained CI while scanning
  every tracked file and post-boundary history blob.

## Upgrade

For an existing direct installation:

```bash
reconc update
reconc doctor --global
```

Exact native install on macOS or Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.2/install.sh \
  | sh -s -- --version 0.9.2
export PATH="$HOME/.local/bin:$PATH"
reconc doctor --global
```

Exact native install on Windows x64:

```powershell
$installer = Join-Path $env:TEMP "reconc-install.ps1"
Invoke-WebRequest https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.2/install.ps1 -OutFile $installer
& $installer -Version 0.9.2
Remove-Item $installer
$env:Path = "$env:LOCALAPPDATA\Programs\Reconc\bin;$env:Path"
reconc doctor --global
```

A global CLI update does not rewrite repository-owned files. Review and apply
repository changes explicitly with `reconc repo sync plan`, `reconc repo sync
apply`, and `reconc repo sync verify`. Reinstall a specific agent hook only
when that repository should receive its updated generated adapter.

## Compatibility And Limits

- The v0.9.1 schema URLs remain the immutable canonical identities for the
  compatible v1 artifact schemas and v3 policy-lock schema.
- Direct installers remain available for macOS amd64/arm64, Linux amd64/arm64,
  and Windows amd64. Windows arm64 is not shipped.
- Kimi Code hooks are user-global and opt-in. Static configuration is not live
  enforcement proof, host timeouts fail open, and post-tool output has no
  authoritative exit status.
- Cursor surfaces do not promise identical hook delivery. `workspaceOpen`
  proves artifact loading only, and Cursor exposes no generic tool hook for
  `AskQuestion`.
- Reconc remains a deterministic repository control layer, not an
  operating-system sandbox against a hostile same-user process.
- Direct POSIX, direct Windows, and source installation are the only supported
  distribution channels in this release.
