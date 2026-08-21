# TASK 242: Close MCP observer and catalog resource gaps

## Why

Two current MCP gateway paths can exceed their lifecycle or memory contracts.
An observer wait failure leaves a pending call registered while retaining the
global send mutex, permanently blocking later calls. Tool discovery retains up
to 64 individually bounded pages before enforcing the aggregate 8 MiB catalog
limit, so adversarial input can allocate far beyond the declared boundary.

## Acceptance

- Every exit after `protocolObserver.begin` consumes the call exactly once by
  observation completion or cancellation and releases `sendMu` exactly once.
- Timeout, cancellation, malformed observed response, SDK mismatch, and decode
  failure leave `pending`, `pendingMethods`, and `byID` empty.
- A second list or call operation proceeds after the first operation's observer
  wait is cancelled; regression coverage fails under the old implementation.
- Tool discovery enforces page count, tool count, per-page count, frame size,
  and aggregate catalog bytes before retaining the next page.
- Peak retained catalog bytes are bounded by the documented aggregate plus one
  bounded in-flight page, not `MaxToolPages * MaxFrameBytes`.
- Existing strict wire-vs-SDK equality, cursor-cycle, schema, icon, and catalog
  validation stays fail closed.
- Go 1.27 `testing/synctest` is used only for isolated channel/timer lifecycle
  tests where it removes wall-clock sleeps; mutex and real I/O behavior remains
  tested through explicit synchronization and bounded contexts.
- Focused MCP tests, race tests, goroutine-leak checks, benchmarks, and gateway
  documentation pass.

## Sub-Tasks

- [ ] Model every protocol-observer ownership transition and terminal path
- [ ] Cancel pending calls on all post-begin errors without double-unlock races
- [ ] Enforce catalog cardinality and byte budgets incrementally
- [ ] Add deterministic timeout, cancellation, reuse, and hostile-pagination tests
- [ ] Add allocation and goroutine-leak regression measurements
- [ ] Run focused, race, integration, and full repository gates
- [ ] Document the exact observer and catalog resource contracts

## Notes

- External findings: F-57 and F-65.
- F-64 is excluded: icon payload bytes, dimensions, total pixels, and icon count
  are already bounded, and decoding is sequential. Full decode is retained to
  prove that the declared raster is actually decodable.
- The smallest safe code fix for the deadlock is cancellation on each
  `waitObserved` error path, but tests must prove exact-once ownership because a
  second cancellation or unlock would panic.

## Deviations

None.
