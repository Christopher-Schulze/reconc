# TASK 407: Bound MCP progress admission

## Why

Progress routing clones up to the full frame limit before enforcing the 64 KiB event limit, and config validation assigns a timeout default into a discarded value copy.

## Acceptance

- Oversized progress frames are rejected before payload cloning or queue admission.
- Progress delivery remains bound to the live request lifecycle and aggregate limits.
- Timeout defaulting has one owner and validation clearly permits zero-as-default.
- Adversarial progress tests and allocation benchmarks cover per-event, aggregate, cancellation, and queue boundaries.

## Sub-Tasks

- [ ] Move progress byte admission before cloning and bind cancellation correctly.
- [ ] Remove discarded config mutation and centralize default application.
- [ ] Add oversized, late-delivery, cancellation, and timeout-default regressions.
- [ ] Run focused MCP frame, progress, gateway tests, and benchmarks.

## Notes

- Verified from findings 78 and 79.
- Finding 77 was withdrawn after reading `action.Decimal`: numeric JSON values are canonicalized, `1.0` becomes `1`, and a negative exponent exactly identifies a non-integer canonical value; the existing frame test proves that contract.
- Finding 76 was rejected after caller inspection: both SDK call sites cancel the observer token on request failure, releasing `sendMu`; no unpaired production path was demonstrated.

## Deviations
