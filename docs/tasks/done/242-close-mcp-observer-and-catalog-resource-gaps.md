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

- [x] Model every protocol-observer ownership transition and terminal path
- [x] Cancel pending calls on all post-begin errors without double-unlock races
- [x] Enforce catalog cardinality and byte budgets incrementally
- [x] Add deterministic timeout, cancellation, reuse, and hostile-pagination tests
- [x] Add allocation and goroutine-leak regression measurements
- [x] Run focused, race, integration, and full repository gates
- [x] Document the exact observer and catalog resource contracts

## Notes

- External findings: F-57 and F-65.
- F-64 is excluded: icon payload bytes, dimensions, total pixels, and icon count
  are already bounded, and decoding is sequential. Full decode is retained to
  prove that the declared raster is actually decodable.
- The smallest safe code fix for the deadlock is cancellation on each
  `waitObserved` error path, but tests must prove exact-once ownership because a
  second cancellation or unlock would panic.
- Observer ownership has three terminal states: pending cancellation removes
  the request and releases `sendMu`; bound cancellation removes only `byID`;
  inbound completion removes `byID`, invokes completion, and publishes exactly
  one buffered response. The new wait wrapper invokes idempotent cancellation
  only on errors, so all three interleavings remain exact-once.
- Catalog validation now owns one scanner, schema cache, name set, byte count,
  tool count, and canonical-contract slice across pages. Each returned page is
  charged and validated immediately; raw page slices are never accumulated by
  discovery.
- Focused verification passed: the complete MCP package in 13.7 seconds, its
  race suite in 28.0 seconds, and the existing runtime goroutine-leak profile.
  The maximum 512-tool/four-page benchmark completed one measured iteration in
  11.2 milliseconds with 5.39 MiB and 54,717 allocations on Apple M1; this is
  measurement evidence, not a portable threshold.
- Final verification passed `make test` including root/template race suites and
  release trust, `make vet`, pinned Staticcheck, the clean self-hosting path,
  root/template module-tidy diffs, and the pinned LangChain interoperability
  proof on Python 3.13.14. No release or tag was created.
- The final decode-failure and SDK-mismatch state assertions passed focused and
  race execution after the complete gate; they add test coverage only and do
  not change the already verified product snapshot.

## Deviations

None.
