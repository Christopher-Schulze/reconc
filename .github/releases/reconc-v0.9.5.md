# reconc v0.9.5

Reconc v0.9.5 is a compatibility-preserving correctness and release-trust
release. It closes the cache, hook-observation, hostile-input, and false-green
gaps found by auditing the post-v0.9.4 source and its published claims. It does
not introduce a policy or schema migration.

## Added

- Bounded, source-free Oh My Pi `user_python` observations. Hook liveness and
  `hook status` expose a saturating count, latest timestamp,
  repository-relative working directory, code byte size, and context-exclusion
  flag without storing Python source.
- A shared bounded subprocess-output boundary for production helpers, with
  boundary-specific limits and explicit overflow failure.
- Exact parity tests tying command metadata, custom-runtime reservations,
  documented hook kinds, portable workflow-audit routes, and scaffold cache
  inputs to their canonical registries.

## Improved

- Stop report reuse now binds every reachable policy-declared input with the
  same evaluator path semantics and supported content, mode, time, and platform
  identity used by the decision. Applicable native assurance always evaluates
  rather than relying on an incomplete fixed input set.
- Completion captures the exact evaluator inputs, dynamic evidence and
  freshness targets, staged command proofs, temporal freshness, and native
  assurance authority before and after evaluation.
- File-backed audit, run-state, bootstrap, release, SBOM, provenance, and
  retention readers validate bounded complete snapshots and reject links,
  special files, identity changes, and partial over-budget input.
- TASK claim and promotion utilities publish transactionally and refuse
  clobbering or moving targets. Legacy pruning applies safe defaults and cannot
  erase evidence under an empty policy.
- The harness pack includes the complete audited portable safety and parity
  suite, and the formatting gate covers non-ignored new Go files before they
  are added to Git.

## Fixed

- Oversized files, over-budget directories, escaping or nested symlinks,
  special files, and unstable policy inputs can no longer preserve a reusable
  Stop fingerprint through an approximate identity.
- Direct or symlinked FIFO inputs no longer block Stop caching or `hook status`
  on macOS.
- OMP `user_python` metadata is persisted instead of being validated and then
  discarded, and every invoked OMP route is covered by the executable contract.
- Release trust now builds the real shipped `release` target. Generated assets,
  copied assets, target-derived binaries, the manifest, checksums, and verifier
  share canonical inventories, so missing, extra, stale, and drifted output
  fail for the asserted reason.
- Codex guidance consistently states native `SessionEnd` cleanup, and route
  enumeration can no longer drift silently between the registry, CLI,
  documentation, custom runtimes, and portable audit.
- Captured Git, worker, Grok, offline-hook, publication, SBOM, and TASK-helper
  output can no longer grow memory without a boundary or expose partial success
  after overflow.

## Upgrade

For an existing direct installation:

```bash
reconc update
reconc doctor --global
```

Exact native install on macOS or Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.5/install.sh \
  | sh -s -- --version 0.9.5
export PATH="$HOME/.local/bin:$PATH"
reconc doctor --global
```

Exact native install on Windows x64:

```powershell
$installer = Join-Path $env:TEMP "reconc-install.ps1"
Invoke-WebRequest https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.5/install.ps1 -OutFile $installer
& $installer -Version 0.9.5
Remove-Item $installer
$env:Path = "$env:LOCALAPPDATA\Programs\Reconc\bin;$env:Path"
reconc doctor --global
```

A global CLI update does not rewrite repository-owned files. Repositories that
use Oh My Pi must refresh the owned extension to receive the new redacted
Python observation route:

```bash
reconc hook install omp . --json
reconc hook status . --json
```

## Compatibility And Limits

- Policy locks remain format `4`. The v0.9.4
  `schemas/v4/policy-lock.schema.json` URL remains the immutable canonical
  identity; the compatible v1 artifact and older policy-lock schema identities
  remain on their original tags.
- No policy can decide arbitrary Python source. OMP `user_python` is
  observation-only; `user_bash` remains the blocking boundary for shell
  commands the user types.
- Direct installers remain available for macOS amd64/arm64, Linux amd64/arm64,
  and Windows amd64. Windows arm64 is not shipped.
- Static configuration and offline contract fixtures are not live proof that a
  particular host delivered a route. Use `reconc hook status . --json` for
  runtime claims.
- Reconc remains a deterministic repository control layer, not an operating
  system sandbox against a hostile same-user process.
