# reconc v0.9.1

v0.9.1 is the Windows direct-installer reliability patch for the v0.9 CLI
line. It preserves the v0.9.0 policy, repository, ownership, schema, and
embedded harness-pack contracts.

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

Rerun the immutable v0.9.1 native installer or use the existing source-owned
update path. Then run `reconc doctor --global`. No repository receipt, policy
lock, hook, schema, or harness-pack migration is required for this patch.

## Supported Platforms

| Platform | Direct installer |
| --- | --- |
| macOS amd64 | yes |
| macOS arm64 | yes |
| Linux amd64 | yes |
| Linux arm64 | yes |
| Windows amd64 | yes |
| Windows arm64 | no |
