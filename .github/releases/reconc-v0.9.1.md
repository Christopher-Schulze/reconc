# reconc v0.9.1

v0.9.1 consolidates the current v0.9 CLI line. It includes the Windows
direct-installer reliability repair, the one-command update lifecycle, and the
current evidence-bounded Cursor CLI hook contract while preserving v0.9.0
policy, repository, ownership, and schema compatibility.

## Added

- Cursor CLI interactive and print surfaces now use registry-owned event sets
  for session start/end, prompt decisions, generic pre/post tool use, Stop, and
  sessionless workspace liveness.
- Cursor `beforeSubmitPrompt` and `subagentStart` emit their native decision
  response shapes. `workspaceOpen` is privacy-redacted, records only route
  liveness, creates no session evidence, and returns no plugin paths.
- `reconc hook status --json` exposes documented `surface_events` separately
  from installed routes and observed live events. The disposable host probe
  consumes that registry truth instead of maintaining a second Cursor matrix.
- Cursor CLI discovery prefers the official `agent` executable, accepts the
  backward-compatible `cursor-agent` alias, and verifies executable identity
  before reporting the host as discoverable.

## Improved

- `reconc update` now runs the complete ownership-aware update transaction.
  Existing `reconc update check` and `reconc update apply` invocations remain
  compatible.
- The README and technical documentation now present installation, bootstrap,
  repository sync, hooks, evidence, release trust, and operational limits with
  a clearer public structure.
- Cursor's host-side `AskQuestion` hook omission and `subagentStart` deny
  enforcement gap are documented explicitly instead of being represented as
  Reconc guarantees.

## Fixed

- The native PowerShell installer now handles a real HTTPS `Content-Length`
  header as the numeric value PowerShell exposes instead of calling nullable
  members that are unavailable on that value.
- Missing headers remain accepted, while negative or over-limit values fail
  before writing a destination.
- The streamed 2 MiB metadata and 256 MiB binary caps, checksum validation,
  GitHub provenance verification, downgrade protection, and atomic
  binary-plus-receipt publication remain enforced.
- Native Windows CI now exercises the numeric, missing, over-limit, and
  negative header paths. The post-publication live-release job verifies the
  complete tagged installer over HTTPS.

## Install

Exact native install on macOS or Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.1/install.sh \
  | sh -s -- --version 0.9.1
export PATH="$HOME/.local/bin:$PATH"
reconc doctor --global
```

Exact native install on Windows x64:

```powershell
$installer = Join-Path $env:TEMP "reconc-install.ps1"
Invoke-WebRequest https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.1/install.ps1 -OutFile $installer
& $installer -Version 0.9.1
Remove-Item $installer
$env:Path = "$env:LOCALAPPDATA\Programs\Reconc\bin;$env:Path"
reconc doctor --global
```

## Upgrade

Run `reconc update`, rerun the exact v0.9.1 native installer, or use the
existing source-owned update path. Then run `reconc doctor --global`.
Repository policy and schema formats do not require migration. Existing
repositories remain untouched until an operator explicitly applies
`reconc repo sync` or reinstalls the Cursor hook; that explicit rollout is
required to receive the expanded generated Cursor artifact.

## Supported Platforms

| Platform | Direct installer |
| --- | --- |
| macOS amd64 | yes |
| macOS arm64 | yes |
| Linux amd64 | yes |
| Linux arm64 | yes |
| Windows amd64 | yes |
| Windows arm64 | no |
