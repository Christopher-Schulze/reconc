# TASK 468: Complete OMP v18 integration

## Why

Reconc's Oh My Pi adapter remains source-compatible with the installed OMP 18.0.11 runtime, but its compaction recovery covers automatic compaction only, its module-global worker transport can be shared across parent and child session bindings, and its official host fixtures still pin OMP 17.2.4. The public documentation also names two approval events incorrectly and does not state the host-controlled extension-disable boundary.

## Acceptance

- Manual, extension-supplied, and automatic successful OMP compactions reach exactly one pre-compaction and one post-compaction Reconc route without duplicate recovery.
- Every OMP extension binding owns its own worker transport; parent and child session shutdown cannot close or delete each other's transport.
- Deterministic adversarial tests reproduce the prior shared-worker lifetime collision and prove independent shutdown under concurrent session activity.
- OMP contract and host-event fixtures pin installed OMP 18.0.11 at source revision `b8ce33a58911c26bed1d84f0db9a5e2e727c49a2` and cover the exact documented native event names.
- Documentation accurately describes generic compaction coverage, exact approval event names, per-session worker ownership, `--no-extensions`, and the observation-only user-Python trust boundary.
- Focused tests, `make test-fast`, `make vet`, `make lint`, reference-document verification, and `git diff --check` pass.

## Sub-Tasks

- [x] Reverify every affected OMP v18 contract, caller, fixture, and documentation surface.
- [x] Replace auto-only compaction bindings with generic compaction lifecycle coverage.
- [x] Make the OMP worker transport session-owned and add adversarial multi-binding regression coverage.
- [x] Refresh the OMP 18.0.11 fixtures and correct all affected documentation.
- [x] Run focused and complete gates, inspect the full diff, archive the TASK, and commit it.

## Notes

- Installed OMP is Homebrew `omp/18.0.11`; the exact clean source tag resolves to `b8ce33a58911c26bed1d84f0db9a5e2e727c49a2`.
- OMP's `session_before_compact` and `session_compact` events cover manual and automatic successful compaction. `auto_compaction_start` and `auto_compaction_end` describe automatic attempts and would duplicate successful lifecycle observations if registered alongside the generic pair.
- OMP rebinds parent-imported extension factories for normal subagents without re-evaluating the module. Module-level mutable state is therefore shared even though each factory call receives a distinct `ExtensionAPI`.
- `user_python` is a direct user `$`/`$$` execution surface, not an agent tool call. Reconc deliberately records redacted metadata without sending Python source to the policy runtime; changing that trust boundary requires a separate product decision.
- Focused OMP generator, host-fixture, normalizer, compaction-payload, and multi-binding worker tests pass. The adversarial worker test binds one cached extension factory twice at the same repository root, shuts down the parent, and proves the child retains its original distinct worker through a later event and its own shutdown.
- Homebrew installs OMP 18.0.11 as a compiled binary without a TypeScript package tree. Source compatibility was therefore checked against the clean exact tag source plus executable generated-adapter tests, not claimed from an unavailable installed-package typecheck.
- `make test-fast` initially found the expected generated-scaffold drift after the adapter changed. The scaffold was updated surgically from the same generator template, its exact parity test passed, and the complete final `make test-fast` rerun passed.

## Deviations

- `make test` was intentionally not run because it includes the uncached Race suite and release-trust suite. Christopher's standing instruction reserves both heavy gates for an explicitly requested final release/CI run; no test was disabled or deleted.
