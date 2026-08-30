# TASK 422: Synchronize MCP progress shutdown

## Why

Progress admission releases its mutex before sending to the queue, while shutdown can close that queue independently. A permitted enqueue can therefore race with `close` and panic.

## Acceptance

- No enqueue can send after queue closure.
- Shutdown remains bounded and does not deadlock blocked producers or the forwarding worker.
- Deterministic interleaving tests reproduce the former send-on-closed window without timing sleeps.
- Progress ordering, suppression, and bounded admission remain unchanged.

## Sub-Tasks

- [x] Define one queue lifecycle invariant across admission, send, finish, and cancellation.
- [x] Close or drain through an ownership-safe protocol without widening queue capacity.
- [x] Add deterministic concurrency regressions and focused benchmarks.
- [x] Run focused MCP progress tests.

## Notes

- Verified from finding 101 in `internal/mcpgateway/progress.go`: admission was mutex-protected, but the channel send and queue closure both occurred outside that mutex.
- Confirmed in the current source: `prepare` released `callProgress.mu` before `enqueue` selected the channel send, while `finish` could close the same queue concurrently.
- Admission, bounded payload cloning, non-blocking send, and queue closure now share `callProgress.mu`; a separate `closed` state rejects late events without preventing the worker from draining already admitted events.
- The deterministic regression pauses payload cloning after admission, drives `finish` concurrently, and proves the lifecycle lock remains held through send. It also verifies ordered delivery and rejection after closure without timing sleeps.
- `go test ./internal/mcpgateway -count=1 -timeout=30s` passed in 18.478s. Focused lifecycle tests passed in 0.016s.
- Three 100ms benchmark samples measured the queue lifecycle at 60.27-65.06 ns/op, 48 B/op, and 1 alloc/op on darwin/arm64; the queue capacity remains 16.

## Deviations

- Per the operator's short-run instruction, repeated full, race, release-trust, and platform suites were deferred to the single queue-end gate run; no Windows tests were run locally.
