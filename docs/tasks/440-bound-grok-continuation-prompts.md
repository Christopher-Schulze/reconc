# TASK 440: Bound Grok continuation prompts

## Why

The initial Grok ACP prompt is capped at one MiB, but a Stop result is assigned directly as the next prompt without the same validation. Direct ACP execution bypasses the CLI hook-output bound.

## Acceptance

- Every initial and continuation prompt obeys one explicit byte and non-empty contract.
- Oversized continuation output fails closed with a bounded diagnostic and is never sent to Grok.
- UTF-8 and structured Stop response handling remain deterministic at the exact limit.
- Tests cover raw/JSON reasons, exact limit, limit-plus-one, repeated continuations, and malformed output.

## Sub-Tasks

- [ ] Centralize Grok prompt validation for initial and continuation inputs.
- [ ] Bound extracted reasons before ACP request construction.
- [ ] Add boundary and continuation-loop regressions.
- [ ] Run focused Grok ACP tests.

## Notes

- Verified from finding 168 in `internal/grokacp/runner.go`.

## Deviations
