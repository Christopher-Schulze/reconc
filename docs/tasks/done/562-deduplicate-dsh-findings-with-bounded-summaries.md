# TASK 562: Deduplicate DSH findings with bounded summaries

## Why

The current eight-messages-per-category lifetime limit can hide a new policy finding after earlier repeated failures. Deduplication must preserve useful new feedback while bounding memory and output.

## Acceptance

- Repeated identical findings are coalesced by category and finding identity; a different finding remains eligible after any number of repeats.
- Deduplication storage, message size, unique-message bursts, pending examples, and timers have explicit bounds.
- Periodic and disposal summaries disclose suppressed repeats and overflow without retaining full tool inputs or claiming omitted findings were verified.
- Deterministic tests prove distinct findings after repetition, burst limits, summary/reset behavior, eviction, sanitization, and cleanup.
- Docs, scaffold/archive and verification expectations match the behavior; complete relevant gates and a separate commit/push.

## Sub-Tasks

- [x] Replace category counters with bounded finding deduplication and compact summaries.
- [x] Verify behavior under duplicates and distinct-message floods and propagate docs/assets.
- [x] Verify implementation and prepare archived completion.

## Notes

Keep diagnostics in the existing DSH extension. Use bounded identity storage and a fixed reporting window, with a small bounded sample of overflow findings and explicit omitted counts. Repeated messages must not consume the fresh-finding burst allowance. Timer ownership belongs to the extension and shutdown flushes its pending summary once. Tests use a controlled clock/output sink for deterministic window checks and retain generated-extension real-worker coverage.

## Deviations

Verification: 1,000 repeated findings produce one initial message while a distinct finding remains visible; a 300-finding burst respects the 16-message window and reports 285 overflow findings with four latest examples. Tests prove the 256-hash bound, display truncation without identity collisions, eviction, actual timed summaries, and idempotent disposal/timer cleanup. The real Go-worker offline matrix, full `make test-fast build vet lint TEST_PARALLELISM=4`, and targeted DSH race tests pass. Scaffold and pack are byte-identical to the reviewed candidate.

None.
