# TASK 415: Centralize hook-runtime failure adaptation

## Why

Payload read, repository resolution, and normalization failures duplicate the complete Copilot, Grok, and generic fail-closed response cascade. Adding or changing a stdout-decision host in only one branch can silently weaken enforcement.

## Acceptance

- One side-effect-free helper maps a route plus failure into exact host response, exit code, stdout, and stderr behavior.
- Payload-read, root-resolution, normalization, and later equivalent failures all use it.
- Platform-specific allow/block contracts remain byte-for-byte compatible.
- A registry-driven table test covers every host and every failure stage.

## Sub-Tasks

- [ ] Capture current failure outputs as golden fixtures.
- [ ] Extract one typed failure adaptation path.
- [ ] Replace all duplicated cascades and add registry completeness checks.
- [ ] Run focused hook-runtime tests.

## Notes

- Verified from finding 97.
- The goal is one enforcement contract, not a broad rewrite of `runHookRuntimeResolved`.

## Deviations
