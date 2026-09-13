# TASK 558: Bound DSH worker lifecycle and memory

## Why

Audit findings 4-6 reproduce stale-process restart failure, delayed queue cancellation, serialized shutdown drainage, and missing aggregate memory bounds. Finding 8 requires regressions against the generated extension.

## Acceptance

- Old process/stream/write callbacks cannot affect a replacement worker; ambiguous requests are never replayed.
- Cancellation removes queued work promptly; deadlines include queue admission, startup, and exchange.
- Shutdown rejects queued work and cancels active exchange without draining a backlog; graceful shutdown itself is bounded.
- Pending frames have an aggregate byte budget and bounded serialization; terminal observations omit complete input bodies without claiming material effects.
- Deterministic protocol fault tests verify restart, cancellation, deadlines, shutdown, byte/count budgets, and compact observations. Existing real-worker verification remains green.

## Sub-Tasks

- [ ] Implement generation-bound callbacks and explicit bounded admission/queue lifecycle.
- [ ] Compact passive observation envelopes and retain compatible normalization.
- [ ] Add durable transport/resource regressions, document limits, verify tests/build, and archive.

## Notes

Use one worker with explicit queued entries, cancellation ownership, and admission deadlines. Avoid a Promise chain retaining canceled payloads. Count bytes before admission using a bounded JSON walk; retain only the serialized frame. Preserve one-at-a-time worker wire protocol and do not replay failed requests. Keep the existing 64 MiB input contract, with a finite aggregate queue budget. Passive results are observations only and need no file body. Benchmarks measure this changed transport, not inferred whole-product gains.

## Deviations

None.
