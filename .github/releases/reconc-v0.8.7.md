# reconc v0.8.7

`v0.8.7` promotes the complete standalone product line since v0.8.6:
portable completion proofs, a verified native Windows installer, stronger
agent-runtime contracts, explicit MCP side-effect boundaries, and a fully
reconciled public documentation and release surface.

## Portable completion proof

- `reconc proof` exports the same current candidate judged by `reconc done` as
  deterministic JSON or Markdown without running missing commands, refreshing
  policy, or persisting a new decision.
- Proof bundles bind build provenance, policy, Git HEAD/index/worktree
  identity, typed TASK state, current command receipts, violations, and exact
  remediation. Blocked candidates remain valid evidence and exit 2.
- The public `proof-bundle` schema, bounded canonical encoding, digest
  verification, atomic file output, and privacy tests exclude absolute paths,
  user identity, sessions, prompts, transcripts, environment data, and raw
  command arguments.
- `reconc demo` now completes its real isolated block-to-remediation journey by
  exporting and verifying the accepted candidate as a portable proof bundle.

## Agent and MCP integration truth

- Cursor generation is registry-driven across Agent/Cmd+K, Tab, interactive
  and print CLI, and eligible cloud routes. Success, failure, liveness, and
  unsupported event semantics remain surface-specific instead of claiming
  false IDE/CLI parity.
- OpenCode and Kilo Code accept shell success only from an integer zero exit,
  keep policy decisions in Go, and use bounded, generation-deduplicated
  asynchronous idle continuation with explicit fail-open host limitations.
- Configured Cursor, OpenCode, and Kilo MCP tools compile into exact typed
  selectors for repository reads, repository writes, commands, or external
  effects. Malformed or unknown calls cannot become positive evidence;
  Cursor's dedicated pre-hook can additionally deny unclassified calls.
- Host status, doctor output, generated artifacts, contract fixtures, and
  negative probes now distinguish configured, discoverable, loaded, observed,
  enforced, inferred, degraded, shadowed, and unsupported states.

## Policy, proof, and repository hardening

- Policy configuration gains a strict public schema, typed MCP compilation,
  unknown-field rejection, lockfile integrity checks, deterministic migrations,
  and stronger cross-platform path identity handling.
- Source hygiene, substantive proof, TASK lifecycle, shell-command parsing,
  publication, privacy, performance, and host-contract coverage close
  cross-cutting correctness gaps without adding runtime network access.
- Public reward-hacking and specification-gaming language is evidence-bounded:
  Reconc constrains configured repository-visible completion failures and does
  not claim general model truthfulness or operating-system isolation.

## Windows and release trust

- `install.ps1` is now shipped from the immutable release tag for Windows x64.
  It requires HTTPS, validates exactly one SHA-256 manifest entry, optionally
  verifies GitHub provenance, smoke-tests a staged binary, publishes
  atomically, preserves an existing installation on failure, and cleans up.
- Native Windows 2025 CI covers both Go modules, binary smoke tests, and the
  installer's success, malformed-manifest, missing-asset, checksum, execution,
  locked-target, attestation, cleanup, and preservation paths. A separate
  post-publication dispatch verifies the published binary and checksum manifest
  over HTTPS without making prerelease CI depend on an asset that cannot yet
  exist.
- CodeQL, bounded security-only Dependabot updates, structured issue and pull
  request templates, contribution guidance, private vulnerability reporting,
  and protected exact-candidate CI strengthen the public repository surface.
- README, documentation SSOT, architecture reference, command reference,
  frozen RFC corrections, embedded guide, public skill, bootstrap material,
  schemas, workflows, and publication tests have been reconciled against the
  executable source without branding or prose pinning.

## Install

macOS or Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.8.7/install.sh \
  | RECONC_INSTALL_DIR="$HOME/.local/bin" sh -s -- 0.8.7
"$HOME/.local/bin/reconc" demo
```

Windows x64:

```powershell
$installer = Join-Path $env:TEMP "reconc-install.ps1"
Invoke-WebRequest https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.8.7/install.ps1 -OutFile $installer
& $installer 0.8.7
Remove-Item $installer
& "$env:LOCALAPPDATA\Programs\Reconc\bin\reconc.exe" demo
```

Set `RECONC_REQUIRE_ATTESTATION=1` to require `gh attestation verify`. The
runtime remains one offline Go binary with no Node, Bun, model, daemon, Docker,
or network dependency.

## Release artifacts

The release uploads `SHA256SUMS` plus exactly twenty checksum-bound artifacts:

- `reconc-0.8.7-darwin-amd64`
- `reconc-0.8.7-darwin-arm64`
- `reconc-0.8.7-linux-amd64`
- `reconc-0.8.7-linux-arm64`
- `reconc-0.8.7-windows-amd64.exe`
- `install.sh` and `install.ps1`
- Bash, Zsh, and Fish completions plus the generated man page
- six immutable v1 schemas plus the current v2 policy-lock schema
- deterministic SPDX 2.3 and CycloneDX 1.6 SBOMs

Every manifest-listed artifact is checksum-verified before upload and covered
by the release workflow's GitHub build-provenance attestation.
