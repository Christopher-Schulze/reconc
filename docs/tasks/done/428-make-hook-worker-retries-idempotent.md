# TASK 428: Make hook-worker retries idempotent

## Why

After writing an event to the persistent worker, any protocol-level error retries the same event through a one-shot process even though the worker may already have applied it. A single transient worker-start failure also disables persistent-worker attempts for the rest of the host session.

## Acceptance

- An event is never applied twice after an ambiguous worker response.
- Retry identity and acknowledgement semantics are bounded and survive worker restart without persisting raw payloads.
- Worker startup failures use bounded backoff/recovery instead of a permanent session-wide downgrade.
- Deterministic tests cover applied-then-error, malformed response, crash before/after acknowledgement, and transient startup failure.

## Sub-Tasks

- [x] Define request identity and acknowledgement semantics for state-mutating hook events.
- [x] Make ambiguous delivery replay-safe or fail closed without duplicate mutation.
- [x] Add bounded worker recovery with deterministic clocks/hooks.
- [x] Run focused hook-worker protocol tests.

## Notes

- Verified from findings 107 and 133.
- Reverification confirmed that every post-write crash, malformed frame, and
  acknowledged `response.error` entered one-shot fallback even though the Go
  handler could already have persisted the event. Startup also set one
  permanent `workerUnsupported` bit for every non-cancellation failure.
- The client now treats the bounded request ID and exact matching response as
  the delivery acknowledgement. Before an event write, one-shot remains safe;
  after the write starts, ambiguous delivery returns an ordinary route failure
  and never replays or persists the raw payload.
- Request IDs remain monotonic across worker restarts. Transient startup
  failures use capped 100 ms, 500 ms, and 2.5 s backoff with injected clocks in
  tests; only a proven handshake/protocol mismatch disables reuse for the
  current plugin instance.
- Focused CLI worker, generated transport, ambiguous-delivery, acknowledgement,
  restart, startup-backoff, and reference-document checks passed. The final
  queue-wide race, vet, static-analysis, and platform gates remain intentionally
  deferred to the agreed end-of-queue pass.

## Deviations
