# reconc v0.9.0

`v0.9.0` turns the standalone toolkit into one globally installed,
ownership-aware CLI with transactional repository ownership and upgrade.

## Highlights

- `reconc init .` is the canonical non-interactive onboarding command. It
  inspects, selects, plans, applies, receipts, and verifies one create-only
  transaction. `reconc bootstrap .` remains a compatibility alias.
- `reconc doctor --global` reports the real installation owner, channel,
  binary identity, PATH shadows, receipt health, checksum, target, and
  provenance without mutation.
- `reconc update check|apply` supports stable, preview, and exact versions for
  direct installs and requires explicit downgrade intent.
- `reconc uninstall` removes only verified installation-owned global state.
  Repository policy, hooks, TASKs, docs, and evidence remain separate.
- `reconc repo sync plan|apply|verify` upgrades repository-owned Reconc files
  from the portable receipt. Plans are digest-bound, stale-state checked,
  drift-blocking, rollback-capable, and offline.
- The binary embeds the immutable `advanced@1.0.0` public harness pack.
  Initialization and sync no longer depend on a copied source checkout.
- Direct installers support stable, preview, and exact selection, preserve the
  selected channel in the receipt, bound downloads, verify checksums and
  release provenance, reject silent downgrades, and never edit shell profiles.
## Install

Exact native install on macOS or Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.0/install.sh \
  | sh -s -- --version 0.9.0
export PATH="$HOME/.local/bin:$PATH"
reconc doctor --global
```

Exact native install on Windows x64:

```powershell
$installer = Join-Path $env:TEMP "reconc-install.ps1"
Invoke-WebRequest https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.0/install.ps1 -OutFile $installer
& $installer -Version 0.9.0
Remove-Item $installer
$env:Path = "$env:LOCALAPPDATA\Programs\Reconc\bin;$env:Path"
reconc doctor --global
```

## Upgrade From v0.8.8

1. Use the exact v0.9.0 native installer for direct installations or a
   path-qualified v0.9.0 `install-cli` for source builds.
2. Run `reconc doctor --global`. Resolve any PATH shadow before repository
   mutation.
3. Plan the repository upgrade with
   `reconc repo sync plan . --output /tmp/reconc-v0.9-sync.json`.
4. Review every action, then apply the exact emitted digest with
   `reconc repo sync apply --plan /tmp/reconc-v0.9-sync.json --digest SHA256`.
5. Run `reconc repo sync verify .`, `reconc status .`, and
   `reconc hook status . --json`.

Legacy private bootstrap receipts are accepted only as bounded migration
evidence. They do not grant ownership of user-edited policy, instructions,
docs, TASKs, or unrelated files. Drift and orphaned legacy paths stop before
mutation and receive explicit review actions.

## Compatibility And Breaking Boundaries

- The old positional installer version remains accepted, but stable is now the
  default channel when no selector is supplied.
- Exact downgrades now require `--allow-downgrade` on POSIX,
  `-AllowDowngrade` on PowerShell, or the equivalent CLI update flag.
- A direct or source install becomes globally owned only after the installed
  binary is the exact bare `reconc` PATH identity. An off-PATH binary is not
  falsely receipted.
- `init --force` is rejected. Managed-block acceptance remains explicit and
  whole-file drift is never overwritten.

## Release Trust

The release contains five native binaries, both native installers,
Bash/Zsh/Fish completions, the generated man page, all public schemas, the
advanced harness pack, deterministic SPDX 2.3 and CycloneDX 1.6 SBOMs,
`release-manifest.json`, and `SHA256SUMS`.

Every checksummed artifact is tied to the protected tag by GitHub build
provenance. The release workflow stays manual-only and draft-first, verifies
the complete inventory before publication, and rejects stale generated
surfaces.

## Supported Platforms

| Platform | Direct installer |
| --- | --- |
| macOS amd64 | yes |
| macOS arm64 | yes |
| Linux amd64 | yes |
| Linux arm64 | yes |
| Windows amd64 | yes |
| Windows arm64 | no |

## Known Limits

- Windows arm64 has no native v0.9 artifact and is rejected explicitly.
- Generated shell hook wrappers and shell policy scripts on Windows require
  `sh`; Git for Windows provides the supported path.
- GitHub attestation verification is optional when `gh` is absent unless
  `RECONC_REQUIRE_ATTESTATION=1` is set.
- Reconc is not an operating-system sandbox. A hostile same-user process still
  requires an external sandbox and protected remote CI.
- There is no background updater, daemon, telemetry, shell-profile mutation,
  implicit privilege elevation, or mutable-`main` installer path.
