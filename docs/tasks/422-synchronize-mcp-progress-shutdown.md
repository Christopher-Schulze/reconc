# TASK 422: Synchronize MCP progress shutdown

## Why

Progress admission releases its mutex before sending to the queue, while shutdown closes that queue under the same mutex. A permitted enqueue can therefore race with `close` and panic.

## Acceptance

- No enqueue can send after queue closure.
- Shutdown remains bounded and does not deadlock blocked producers or the forwarding worker.
- Deterministic interleaving tests reproduce the former send-on-closed window without timing sleeps.
- Progress ordering, suppression, and bounded admission remain unchanged.

## Sub-Tasks

- [ ] Define one queue lifecycle invariant across admission, send, finish, and cancellation.
- [ ] Close or drain through an ownership-safe protocol without widening queue capacity.
- [ ] Add deterministic concurrency regressions and focused benchmarks.
- [ ] Run focused MCP progress tests.

## Notes

- Verified from finding 101 in `internal/mcpgateway/progress.go`: admission and closure are synchronized, but the channel send occurs after the lock is released.

## Deviations
