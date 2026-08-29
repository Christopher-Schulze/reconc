# TASK 428: Make hook-worker retries idempotent

## Why

After writing an event to the persistent worker, any protocol-level error retries the same event through a one-shot process even though the worker may already have applied it. A single transient worker-start failure also disables persistent-worker attempts for the rest of the host session.

## Acceptance

- An event is never applied twice after an ambiguous worker response.
- Retry identity and acknowledgement semantics are bounded and survive worker restart without persisting raw payloads.
- Worker startup failures use bounded backoff/recovery instead of a permanent session-wide downgrade.
- Deterministic tests cover applied-then-error, malformed response, crash before/after acknowledgement, and transient startup failure.

## Sub-Tasks

- [ ] Define request identity and acknowledgement semantics for state-mutating hook events.
- [ ] Make ambiguous delivery replay-safe or fail closed without duplicate mutation.
- [ ] Add bounded worker recovery with deterministic clocks/hooks.
- [ ] Run focused hook-worker protocol tests.

## Notes

- Verified from findings 107 and 133.

## Deviations
