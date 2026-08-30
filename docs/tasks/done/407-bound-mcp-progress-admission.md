# TASK 407: Bound MCP progress admission

## Why

Progress routing clones up to the full frame limit before enforcing the 64 KiB event limit, and config validation assigns a timeout default into a discarded value copy.

## Acceptance

- Oversized progress frames are rejected before payload cloning or queue admission.
- Progress delivery remains bound to the live request lifecycle and aggregate limits.
- Timeout defaulting has one owner and validation clearly permits zero-as-default.
- Adversarial progress tests and allocation benchmarks cover per-event, aggregate, cancellation, and queue boundaries.

## Sub-Tasks

- [x] Move progress byte admission before cloning and bind cancellation correctly.
- [x] Remove discarded config mutation and centralize default application.
- [x] Add oversized, late-delivery, cancellation, and timeout-default regressions.
- [x] Run focused MCP frame, progress, gateway tests, and benchmarks.

## Notes

- Verified from findings 78 and 79.
- Finding 77 was withdrawn after reading `action.Decimal`: numeric JSON values are canonicalized, `1.0` becomes `1`, and a negative exponent exactly identifies a non-integer canonical value; the existing frame test proves that contract.
- Finding 76 was rejected after caller inspection: both SDK call sites cancel the observer token on request failure, releasing `sendMu`; no unpaired production path was demonstrated.
- Confirmed in current code: `routeProgressFrame` cloned `frame.params` before `callProgress.admit` enforced the 64 KiB event limit, allowing a valid near-10 MiB frame to allocate before rejection.
- Progress payload ownership now moves into `callProgress.prepare`: lifecycle, per-event, event-count, and aggregate-byte admission happens before the accepted payload is cloned and queued. Workers recheck call cancellation before handling a dequeued event.
- Validation now explicitly permits `CallTimeout == 0`; `effectiveCallTimeout` is the single default owner used by gateway construction and defensive call-context creation.
- Regressions cover oversized no-clone admission, accepted clone ownership, aggregate/event/queue limits, cancellation, late unregister, and zero timeout. The focused allocation benchmark measured accepted events at 65,712 B/op and oversized events at 176 B/op across three runs.
- Verification passed: `go test ./internal/mcpgateway -count=1`, `go test ./internal/mcpgateway -run '^$' -bench '^BenchmarkCallProgressAdmission$' -benchmem -count=3`, `make test-fast`, `gofmt`, and `git diff --check`.

## Deviations
