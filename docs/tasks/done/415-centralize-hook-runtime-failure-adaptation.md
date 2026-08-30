# TASK 415: Centralize hook-runtime failure adaptation

## Why

Payload read, repository resolution, and normalization failures duplicate the complete Copilot, Grok, and generic fail-closed response cascade. Adding or changing a stdout-decision host in only one branch can silently weaken enforcement.

## Acceptance

- One side-effect-free helper maps a route plus failure into exact host response, exit code, stdout, and stderr behavior.
- Payload-read, root-resolution, normalization, and later equivalent failures all use it.
- Platform-specific allow/block contracts remain byte-for-byte compatible.
- A registry-driven table test covers every host and every failure stage.

## Sub-Tasks

- [x] Capture current failure outputs as golden fixtures.
- [x] Extract one typed failure adaptation path.
- [x] Replace all duplicated cascades and add registry completeness checks.
- [x] Run focused hook-runtime tests.

## Notes

- Verified from finding 97.
- The goal is one enforcement contract, not a broad rewrite of `runHookRuntimeResolved`.
- Confirmed on current source: payload-read, root-resolution, and payload-normalization failures each repeat the same four-way dispatch for Copilot Stop/SubagentStop JSON blocks, Grok PreToolUse JSON denials, generic fail-closed exits, and fail-open warnings.
- The route registry already provides the complete platform, neutral event, and error-policy inputs. A typed failure stage plus one pure adapter can own the exact transport result without changing handler execution or post-handler adapters.
- The registry-driven golden test exercises every registered runtime route at every failure stage, verifies every agent platform is represented, and pins exit codes, stdout, stderr, CLI errors, Copilot/Grok JSON bytes, and decision-write failures.
- Verification passed: focused boundary-adaptation tests, the complete `internal/cli` package, and `make test-fast`.

## Deviations
