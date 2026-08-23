# TASK 287: Repair update isolation and replace v0.9.7

## Why

The POSIX release-trust harness invokes the real installer without consistently
isolating `RECONC_HOME`. A successful temporary installer fixture therefore
overwrote the operator's real installation receipt with a temporary binary
path. The ownership-safe updater correctly refuses that stale receipt, so the
installed v0.9.6 binary cannot currently apply the published v0.9.7 update.
The existing v0.9.7 release also predates the post-release CodeQL corrections
on `main` and must be replaced without changing the product version.

## Acceptance

- Every release-trust installer invocation uses a task-owned `RECONC_HOME` and
  cannot write the operator's installation state.
- Regression coverage proves a same-version replacement is discovered and
  atomically applied when the selected artifact digest changes.
- The complete local release, race, static-analysis, and publication gates pass
  without changing the operator's pre-gate receipt.
- `main` remains the only branch, stays at product version v0.9.7, and passes
  fresh GitHub CI and CodeQL.
- The existing `reconc-v0.9.7` release is replaced from the latest verified
  `main` commit with valid checksums, manifests, attestations, and artifacts.
- The system installation updates through `reconc update` to the replacement
  v0.9.7 artifact and `reconc doctor --global` reports healthy direct ownership.

## Sub-Tasks

- [x] Isolate every POSIX release-trust installer invocation
- [x] Strengthen same-version update regression coverage
- [x] Synchronize release and operator documentation
- [x] Run focused and complete local verification without receipt drift
- [x] Commit and push the verified v0.9.7 source to `main`
- [x] Require fresh green CI and CodeQL on the exact source commit
- [x] Repair the existing direct installation through the verified installer
- [x] Detect the exact-receipt default-channel refusal through a real update
- [x] Make bare `reconc update` select stable from an exact installation
- [x] Commit the exact-channel repair and require fresh green CI and CodeQL
- [x] Detect the producer/consumer release-asset mismatch through a real update
- [x] Align the Zsh asset and validate producer manifests with consumer rules
- [x] Commit the compatibility repair and require fresh green CI and CodeQL
- [x] Replace and verify the published `reconc-v0.9.7` release
- [x] Apply the replacement and verify future bare `reconc update`
- [x] Archive the completed TASK

## Notes

- Live GitHub state before implementation reports `reconc-v0.9.7` as the
  latest stable release; no v0.9.8 release or release process exists.
- The damaged receipt claims v0.9.7 at a deleted release-trust temporary path,
  while the real PATH binary is a valid v0.9.6 release at
  `~/.local/bin/reconc`.
- `scripts/tests/release-trust.sh` supplies `RECONC_HOME` only for one explicit
  installer call. Its shared `run_installer` helper omits the variable and can
  therefore write `~/.reconc/install/receipt.json` whenever its temporary
  install directory is visible on PATH.
- The first complete race pass exposed a second independent leak in
  `TestInstallCLICommandPublishesAReadyBareCommand`: it isolated the install
  directory and PATH but not `RECONC_HOME`, publishing a test receipt into the
  operator's real state. The test now owns a temporary Reconc home as well.
- The CLI package's shared `TestMain` also established a temporary install
  directory and PATH without a default temporary `RECONC_HOME`. Package-level
  isolation now covers every CLI test, including tests that do not override the
  shared environment explicitly.
- Updater refusal is the intended ownership safety boundary. Recovery should
  restore a verified direct receipt rather than weaken stale-receipt checks.
- Verification passed for the same-version check-and-apply regression, the
  complete `internal/usercli` race package, all root race packages after
  focused repair of the two failing packages, the complete portable-template
  race suite, Release Trust, Vet, Staticcheck, Tidy diff, Govulncheck with no
  findings, Publication Audit, Self-Host, and the pinned LangChain proof on
  Python 3.13.14. Hard before/after hashing proved the operator receipt stayed
  unchanged after the final CLI and release-trust isolation repairs.
- Commit `bbd6e84a09cc7331cff6fa38cba57f90c21f95ab` passed CI run
  `32638212780`, including the native Windows full suite, and CodeQL run
  `32638212773`.
- The currently published v0.9.7 installer restored healthy direct ownership at
  `~/.local/bin/reconc`; `reconc doctor --global` reports exact checksum
  identity, no PATH shadows, and GitHub-verified provenance.
- Release run `32638977670` replaced v0.9.7 from commit `bbd6e84a` and passed
  every gate, but the required real bare update then exposed a remaining UX
  defect: an exact-version receipt refused the documented stable default unless
  `--channel stable` was repeated. The final replacement must include the
  narrow exact-to-stable default repair and preserve explicit preview exit.
- Release run `32641194751` then passed every gate from commit `1878ee3e`, but
  the old installed updater rejected the live manifest because the generated
  Zsh asset `_reconc` violated its alphanumeric-first asset-name contract. The
  producer accepted a name the consumer rejected. The final compatible release
  uses `reconc.zsh` and makes manifest generation call the consumer validator.
- Compatibility commit `9fb4b7e059e5f5635c0c10ca08851c52fcc73f8f`
  passed CI run `32643451333` and CodeQL run `32643451309`; the repository had
  zero open code-scanning alerts.
- Release run `32643982614` rebuilt and published `reconc-v0.9.7` from
  `9fb4b7e0`. All three jobs passed, including the native Windows and LangChain
  gates. The protected annotated tag dereferences to that exact commit.
- Independent publication verification found 54 assets, `reconc.zsh`, no
  legacy `_reconc`, a matching Darwin arm64 SHA-256 digest
  `9a43200f3860a6b2572c5172f2b98162d2d1445e31b1037b34931be83e57a6cf`,
  and valid GitHub attestations for the binary and release manifest.
- The installed direct CLI updated from the prior v0.9.7 digest
  `035c2af4464bc37c865cbe8cf4c4528ca538f8f505e698c3c25602942b5f651b`
  to the replacement v0.9.7 digest through the one-time explicit stable bridge
  required by the immutable old updater. `reconc doctor --global --json` then
  reported `healthy`, direct ownership, checksum identity, GitHub-verified
  provenance, and no PATH shadows. A subsequent bare `reconc update` returned
  `Status: current` without mutation.

## Deviations

None.
