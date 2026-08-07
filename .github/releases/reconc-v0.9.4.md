# reconc v0.9.4

Reconc v0.9.4 moves the policy lockfile to format version `4` and closes the
host-integration gaps found by verifying every supported runtime against its
own published configuration surface. Format 1, 2, and 3 lockfiles migrate
automatically on read, so no repository action is required.

## Added

- `cache_inputs` on `require_script` rules and on `require_script` checks inside
  composite rules. A gate declares the literal repository-relative files its
  script reads, and Stop report reuse binds exactly those files. A gate that
  declares nothing is never reused and runs on every Stop.
- MCP policy on Claude Code and Codex. Both hosts publish MCP calls as
  `mcp__<server>__<tool>` on their generic tool events and accept a matcher for
  that namespace, so exact MCP selectors, `unclassified: deny`, and MCP write
  evidence now work there as they already did on Cursor.
- Native `SessionEnd` routing for Codex and native `Notification` routing for
  Claude Code. Both events are part of those hosts' configuration surfaces and
  were previously reported as unsupported.
- Windows executable-shadow reporting across `PATHEXT` in the global diagnostic.

## Improved

- The MCP platform vocabulary, the two live JSON schemas, `hook status`, and
  `doctor --deep` derive from one ordered source, so a supported host cannot be
  accepted by one surface and dropped by another.
- `hook status` and `doctor --deep` report strict unclassified MCP deny as
  available on every host that has a discriminator, instead of naming Cursor
  alone.
- Codex `SessionEnd` declares the three-second timeout that host accepts rather
  than a value it clamps and warns about.
- The Pi contract fixture records package version 0.84.1 and its exact source
  revision. That revision widened the blocking tool result with `terminate`, a
  hint the host honors only under batch unanimity; Reconc has no policy mode
  that ends a session, so the adapter keeps returning `{block, reason}`.
- Host coverage is stated rather than implied: Claude Code accepts 31 hook
  events and Reconc binds the 15 that carry a decision or attributable
  evidence; GitHub Copilot names fourteen and Reconc binds twelve.

## Fixed

- Rooted-path decisions no longer depend on the operating system that evaluates
  them. `filepath.IsAbs` treats a POSIX root as relative on Windows, so a
  declared cache input or a git ref naming `/etc/passwd` was refused on Unix and
  resolved against the repository on Windows. One helper now rejects every
  rooting convention at once, and the policy-file and script resolvers use it.
- `require_script` containment is enforced on the resolved parent directory, so
  an intermediate directory symlink can no longer move execution outside the
  repository while every path segment stays a plain name.
- `kill_timeout_sec` values that overflowed the raw conversion no longer wrap
  into a negative wait delay that disabled SIGKILL escalation.
- `forbid_command` no longer misses escaped or quoted executables such as `\rm`.
- Oversize hook output keeps the real reason and appends the byte-budget notice
  instead of replacing the diagnostic.
- Composite `require_script` checks validate their `cache_inputs` like top-level
  rules do.

## Upgrade

For an existing direct installation:

```bash
reconc update
reconc doctor --global
```

Exact native install on macOS or Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.4/install.sh \
  | sh -s -- --version 0.9.4
export PATH="$HOME/.local/bin:$PATH"
reconc doctor --global
```

Exact native install on Windows x64:

```powershell
$installer = Join-Path $env:TEMP "reconc-install.ps1"
Invoke-WebRequest https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.4/install.ps1 -OutFile $installer
& $installer -Version 0.9.4
Remove-Item $installer
$env:Path = "$env:LOCALAPPDATA\Programs\Reconc\bin;$env:Path"
reconc doctor --global
```

A global CLI update does not rewrite repository-owned files. Repositories that
installed Claude Code or Codex hooks before this release keep working, and their
installed artifacts report as stale until they are reinstalled:

```bash
reconc hook status . --json
reconc hook install claude-code . --json
reconc hook install codex . --json
```

## Compatibility And Limits

- Format 4 is published as the v0.9.4 `schemas/v4/policy-lock.schema.json`
  identity. The v0.9.1 schema URLs remain the immutable canonical identities for
  the compatible v1 artifact schemas and for the v1, v2, and v3 policy-lock
  schemas.
- `cache_inputs` accepts literal repository-relative files and directories.
  Globs, template variables, escaping paths, and duplicates are refused at
  compile time, because binding them would require a directory walk on the Stop
  path.
- Direct installers remain available for macOS amd64/arm64, Linux amd64/arm64,
  and Windows amd64. Windows arm64 is not shipped.
- MCP enforcement before execution requires a host that can tell an MCP call
  apart from a built-in tool: a dedicated MCP event on Cursor, the `mcp__`
  namespace on Claude Code and Codex. OpenCode, Kilo, OMP, Pi, and ZCode enforce
  configured identities but report strict unclassified deny as unavailable.
- Static configuration and offline contract fixtures are not live proof that a
  particular host binary delivered a hook. Use `reconc hook status . --json` for
  runtime claims.
- Reconc remains a deterministic repository control layer, not an operating
  system sandbox against a hostile same-user process.
