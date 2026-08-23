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
- [~] Commit and push the verified v0.9.7 source to `main`
- [ ] Require fresh green CI and CodeQL on the exact source commit
- [ ] Repair the existing direct installation through the verified installer
- [ ] Replace and verify the published `reconc-v0.9.7` release
- [ ] Apply and verify the replacement through `reconc update`
- [ ] Archive the completed TASK

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

## Deviations

None.
