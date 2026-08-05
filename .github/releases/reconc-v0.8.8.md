# reconc v0.8.8

`v0.8.8` is the final v0.8.x patch baseline before the separately planned
v0.9.0 CLI productization. It publishes the complete standalone line since
v0.8.7: lossless bounded-evidence continuity, explicit taint recovery,
reachable lockfile repair, a stable user CLI installation contract, stronger
cross-platform behavior, and materially higher test coverage.

## Evidence continuity and termination

- Long agent sessions now seal complete raw evidence into bounded,
  SHA-256-linked segments instead of truncating the evidence window. Every
  policy, claim, CI, Stop, and completion consumer verifies and replays the
  complete chain plus live evidence.
- Full-chain replay deduplicates evidence in linear time and validates
  multi-segment links, so rotation stays operational under sustained command
  load without turning summaries into synthetic proof.
- Evidence that cannot fit an empty segment, segment exhaustion, storage
  failure, or chain corruption creates a durable project-scoped taint with the
  exact field and limit cause. Successor sessions inherit it, material actions
  remain blocked, and no certified policy or completion pass is possible.
- With repository run disabled, a tainted session may terminate only as
  explicitly uncertified. Run-enabled Stop remains blocked. Recovery requires
  `reconc hook evidence-status` followed by token-bound
  `reconc hook evidence-resolve` with an operator reason.
- Session-state readers and the shared active-session pointer now use explicit
  cross-process serialization, including native Windows sharing semantics and
  one-way lock ordering.

## Policy and workflow recovery

- Stale-lockfile blocks admit only a fully parsed `reconc refresh` or
  `reconc compile` invocation while all other gated work remains blocked.
  Compound commands, pipes, dynamic executables, and unrelated chained work
  cannot inherit the repair exemption.
- Shell-analysis failures now report the exact bounded cause and concrete
  remediation instead of one generic refusal.
- Repository cleanliness scans use a dedicated classified timeout, and
  publication root identity uses the operating system's canonical filesystem
  identity instead of case-sensitive string equality.
- Portable workflow audits distinguish unreferenced TASK details from arbitrary
  Markdown in reserved TASK directories and keep remediation explicit.

## Stable user CLI and bootstrap

- `reconc install-cli` atomically installs the exact running executable into
  the stable user command location, rejects unsafe targets, verifies the
  installed checksum and executable mode, and proves bare `reconc` resolves to
  that build.
- Mutating compatibility and transactional bootstrap establish the same user
  CLI contract before repository writes. Bootstrap verification checks it
  again, and both native installers emit exact PATH remediation when the
  installed command is missing or shadowed.
- Run-control guidance now uses the canonical repository-root form
  `reconc run on|status|off`, so users and agents no longer need versioned or
  repository-local binary paths for routine operation.

## Test breadth and portability

- Whole-module coverage is measured for both Go modules as review evidence;
  pass or fail is determined by substantive tests, not a numeric target.
- Strict behavioral tests add positive, negative, malformed, boundary,
  concurrency, rollback, parser, CLI, bootstrap, runtime, audit, and
  publication coverage without exclusions or denominator tricks.
- Windows-native regressions cover user-CLI cleanup, PATH guidance, TASK
  reference normalization, concurrent session state, and installer behavior.
  Canonical-path and template tests remain platform-neutral on macOS, Linux,
  and Windows.

## Install

macOS or Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.8.8/install.sh \
  | sh -s -- 0.8.8
export PATH="$HOME/.local/bin:$PATH"
reconc --version
```

Windows x64:

```powershell
$installer = Join-Path $env:TEMP "reconc-install.ps1"
Invoke-WebRequest https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.8.8/install.ps1 -OutFile $installer
& $installer 0.8.8
Remove-Item $installer
$env:Path = "$env:LOCALAPPDATA\Programs\Reconc\bin;$env:Path"
reconc --version
```

Set `RECONC_REQUIRE_ATTESTATION=1` to require `gh attestation verify`. Core
repository control remains one offline Go binary with no Node, Bun, model,
daemon, Docker, or runtime network dependency.

## Release artifacts

The release uploads `SHA256SUMS` plus exactly twenty checksum-bound artifacts:

- `reconc-0.8.8-darwin-amd64`
- `reconc-0.8.8-darwin-arm64`
- `reconc-0.8.8-linux-amd64`
- `reconc-0.8.8-linux-arm64`
- `reconc-0.8.8-windows-amd64.exe`
- `install.sh` and `install.ps1`
- Bash, Zsh, and Fish completions plus the generated man page
- six immutable v1 schemas plus the current v2 policy-lock schema
- deterministic SPDX 2.3 and CycloneDX 1.6 SBOMs

Every manifest-listed artifact is checksum-verified before upload and covered
by the release workflow's GitHub build-provenance attestation.
