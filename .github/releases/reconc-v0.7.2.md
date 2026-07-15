# reconc v0.7.2

`v0.7.2` hardens audit truth, release supply-chain evidence, native Windows
execution, remote branch governance, and CLI maintainability without changing
the public command surface.

## Reliability And Security

- Audit-cache tree walks and TASK diff collection now fail closed when their
  authority surface is incomplete. An unreadable tree or failed staged/working
  diff can no longer publish or reuse a false pass.
- Lockfile readers decode directly from the existing byte slice, removing one
  full lockfile copy per policy check and TUI summary. The measured check path
  saves one allocation and about 1.8 KiB per operation; wall-time remains
  filesystem-dependent and is not claimed as a stable percentage.
- The public `main` branch is protected against deletion and force pushes and
  requires Ubuntu, macOS, native Windows, and release-trust checks from GitHub
  Actions before it can advance.

## Native Windows Parity

- Windows 2025 now runs the full root and `harness/template` test, vet,
  Staticcheck, and race gates plus a native binary smoke check.
- Windows owns native `LockFileEx` TASK transaction locks, portable slash
  normalization, executable-file semantics, rollback identity behavior, and
  `.exe`/`.com` script dispatch. Shell hook wrappers plus `.sh` and
  extensionless policy scripts use `sh` from Git for Windows.
- POSIX rollback retains an open directory identity handle to resist inode
  reuse; Windows uses stable non-blocking file identity so transaction cleanup
  is never prevented by its own handle.

## Release Supply Chain

- Every release now includes deterministic SPDX 2.3 and CycloneDX 1.6 SBOMs
  for both Go modules, selected dependencies, the Go toolchain, release
  version, and exact source commit.
- SBOMs are regenerated and byte-verified before entering `SHA256SUMS` and the
  GitHub build-provenance attestation. Missing, stale, malformed, extra, or
  corrupted artifacts fail the release.

## Maintainability

- The 3,998-line CLI monolith is decomposed into responsibility-owned command
  files. `internal/cli/cli.go` is now a 209-line public error, dispatch, and
  usage owner; the largest newly extracted command owner is 577 lines.
- All 84 original declarations were moved exactly once with no compatibility
  router or wrapper. Golden comparison proves identical top-level help and
  exit codes, global usage, Bash/Zsh/Fish completions, manpage, and JSON version
  output.

## Release Artifacts

- `reconc-0.7.2-darwin-amd64`
- `reconc-0.7.2-darwin-arm64`
- `reconc-0.7.2-linux-amd64`
- `reconc-0.7.2-linux-arm64`
- `reconc-0.7.2-windows-amd64.exe`
- deterministic SPDX 2.3 and CycloneDX 1.6 SBOMs
- Bash, Zsh, and Fish completions
- man page
- three public v1 JSON schemas
- `SHA256SUMS`
