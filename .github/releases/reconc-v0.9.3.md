# reconc v0.9.3

Reconc v0.9.3 is a compatibility-preserving quality release. It completes the
native ZCode runtime surface, tightens malformed-input handling and strict
outcome coverage, and brings the public documentation and release metadata to
one consistent current state. It does not introduce a policy or schema
migration.

## Added

- Native ZCode support through project-local `.zcode/config.json` integration.
  The generated adapter wires all seven documented events through the
  process-executor transport: `SessionStart`, `UserPromptSubmit`, `PreToolUse`,
  `PermissionRequest`, `PostToolUse`, `PostToolUseFailure`, and `Stop`.
- Regression coverage for duplicate JSON keys, trailing values, non-object
  payloads, invalid JSON Pointers, oversized host payloads, MCP envelopes,
  strict Stop outcomes, passive events, and lockfile mode integrity.
- Current-state migration and installation guidance for the v0.9.3 source and
  release line, including explicit ZCode hook installation and restart
  behavior.

## Improved

- Runtime documentation, command references, generated agent guidance, the
  README, issue template, self-hosting diagnostics, installer examples, and
  release metadata now agree on v0.9.3 and the thirteen supported coding-agent
  runtimes.
- Test-depth reporting remains measurement-only review evidence and never blocks
  a build or release on a numeric result.
- Release provenance fixtures and versioned recovery-path tests now exercise the
  v0.9.3 source line instead of stale v0.9.2 examples.

## Fixed

- Windows Bun hook timeouts now terminate the complete wrapper process tree,
  preventing detached shell descendants from poisoning the next hook event.
- Draft release reconciliation now addresses the release by its immutable
  GitHub release ID, so draft assets can be replaced and verified before
  publication instead of failing on the tag-only API view.
- Removed stale current-release references that could direct users to the
  superseded v0.9.2 installer while leaving historical v0.9.2 migration and
  release notes intact.

## Upgrade

For an existing direct installation:

```bash
reconc update
reconc doctor --global
```

Exact native install on macOS or Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.3/install.sh \
  | sh -s -- --version 0.9.3
export PATH="$HOME/.local/bin:$PATH"
reconc doctor --global
```

Exact native install on Windows x64:

```powershell
$installer = Join-Path $env:TEMP "reconc-install.ps1"
Invoke-WebRequest https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.3/install.ps1 -OutFile $installer
& $installer -Version 0.9.3
Remove-Item $installer
$env:Path = "$env:LOCALAPPDATA\Programs\Reconc\bin;$env:Path"
reconc doctor --global
```

A global CLI update does not rewrite repository-owned files. Install the ZCode
adapter explicitly where needed and restart ZCode so it snapshots the updated
configuration:

```bash
reconc hook install zcode . --json
reconc hook status . --json
```

## Compatibility And Limits

- The v0.9.1 schema URLs remain the immutable canonical identities for the
  compatible v1 artifact schemas and v3 policy-lock schema.
- Direct installers remain available for macOS amd64/arm64, Linux amd64/arm64,
  and Windows amd64. Windows arm64 is not shipped.
- ZCode pre-tool, permission, and synchronous Stop routes can block when the
  host provides the required decision boundary. Host timeouts remain
  ZCode-owned fail-open behavior, and Stop continuation is capped by ZCode at
  three consecutive blocks.
- Static configuration and offline contract fixtures are not live proof that a
  particular ZCode binary delivered a hook. Use `reconc hook status . --json`
  and bounded host verification for runtime claims.
- Reconc remains a deterministic repository control layer, not an operating
  system sandbox against a hostile same-user process.
