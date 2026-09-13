# TASK 560: Make DSH integration non-blocking and fully composable

## Why

The user explicitly rejects Reconc-imposed DSH execution restrictions. Source uncertainty must reduce claimed coverage, never disable host functionality. This supersedes TASK 557's blocking compatibility boundaries for DSH only.

## Acceptance

- Reconc's DSH extension never denies tool dispatch, rejects an agent step, locks host execution properties, or requires a successful policy/worker response to continue.
- Native and PTC tools, persistent shells, PowerShell, terminals, renamed delegation, external providers, other compatibility plugins, and alternate working directories remain usable.
- Reconc evaluates available events and reports findings as bounded advisory diagnostics. Uncertain tool effects never become fabricated repository evidence.
- Worker lifecycle, memory limits, cancellation, and managed patch preservation remain intact. Direct Reconc CLI and CI policy enforcement remains unchanged.
- Existing generated/scaffold/pack artifacts, capability metadata, installation guidance, documentation, skill references, and offline tests match the non-blocking contract.
- Source inspection and offline tests provide acceptance; no DSH installation or host/model run. Relevant complete gates pass before commit/push to origin/main.

## Sub-Tasks

- [x] Replace DSH dispatch restrictions with bounded advisory evaluation and source-aligned tool normalization.
- [x] Propagate metadata, generated assets, documentation, and meaningful offline compatibility regressions.
- [x] Verify the consolidated implementation and prepare the archived completion record.

## Notes

Upstream source: deepseek-ai/deepseek-harness at c291e7961a515f6d7af9304e7fd1d257929aef26, with the existing pinned release contract fb2c4b9e698e30edb738bca4cf0618587db7d203. `tools/pre-execute` is a next-based waterfall; `tools/result` is a contained passive emitter. PTC nested calls use the normal tool scheduler. Persistent Bash owns state per agent; external providers do not prove inherited Reconc protection. These facts determine observation/evidence scope, not tool availability.

Verification: generated DSH composition, worker lifecycle/resource contracts, real Go-worker feedback, continued dispatch, passive evidence, and shared host-envelope tests pass. The initial whole-module uncached race run passed every package except the shared host test's obsolete DSH exit-block expectation. That test now asserts the advisory envelope; the entire CLI package, portable-template race suite, publication/pack audits, and release-trust checks subsequently passed through `make test PKG=./internal/cli TEST_PARALLELISM=4`. `make build vet lint self-host` and portable skill validation passed. Bun 1.3.14 matches CI. Generated references and archive integrity match the reviewed source. Commit/push and exact-commit GitHub results are reported separately after publication of this completion record.

## Deviations

The user's explicit non-blocking DSH requirement overrides the prior fail-closed DSH adapter design. Other hosts and explicit CLI/CI checks retain their contracts.
