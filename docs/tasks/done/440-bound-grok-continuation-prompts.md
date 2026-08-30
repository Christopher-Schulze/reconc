# TASK 440: Bound Grok continuation prompts

## Why

The initial Grok ACP prompt is capped at one MiB, but a Stop result is assigned directly as the next prompt without the same validation. Direct ACP execution bypasses the CLI hook-output bound.

## Acceptance

- Every initial and continuation prompt obeys one explicit byte and non-empty contract.
- Oversized continuation output fails closed with a bounded diagnostic and is never sent to Grok.
- UTF-8 and structured Stop response handling remain deterministic at the exact limit.
- Tests cover raw/JSON reasons, exact limit, limit-plus-one, repeated continuations, and malformed output.

## Sub-Tasks

- [x] Centralize Grok prompt validation for initial and continuation inputs.
- [x] Bound extracted reasons before ACP request construction.
- [x] Add boundary and continuation-loop regressions.
- [x] Run focused Grok ACP tests.

## Notes

- Verified from finding 168 in `internal/grokacp/runner.go`.
- Confirmed: only `Options.Prompt` was checked; `continuationReason` returned unbounded raw or JSON-derived Stop text directly to both ACP `session/prompt` and optional leader interjection.
- One validator now requires every sent prompt to be non-empty valid UTF-8 at or below 1 MiB. Stop output is rejected above the 1 MiB prompt budget plus a 4 KiB structured-envelope allowance before JSON decoding; JSON-looking malformed output and over-limit extracted reasons fail closed without echoing their contents.
- Regressions cover raw and all supported JSON reason fields, ASCII and multibyte UTF-8 at the exact byte limit, limit-plus-one, envelope overflow, invalid UTF-8, malformed structured output, two successive continuations in one ACP session, and proof that an oversized reason never produces a second ACP request or leader interjection.
- The complete `internal/grokacp` package and focused CLI Grok tests pass locally on darwin/arm64; Windows-specific execution remains assigned to the final CI matrix.

## Deviations
