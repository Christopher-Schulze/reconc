# TASK 417: Preserve exact hook output-envelope limits

## Why

`boundHookResult` accepts an 8 KiB envelope, but runtime emission adds a newline before the worker captures stdout in an 8 KiB buffer. An exactly-at-limit valid deny envelope is therefore truncated and replaced with a generic worker failure.

## Acceptance

- Envelope admission includes every emitted framing byte.
- A deny at the exact boundary reaches stdout as valid complete JSON and retains the intended host decision.
- Oversized output emits a minimal valid bounded deny/error contract rather than truncated JSON.
- Boundary tests cover limit-minus-one, exact limit, limit-plus-one, multi-byte UTF-8, newline, and every stdout-decision host.

## Sub-Tasks

- [ ] Define whether the shared limit includes the terminal newline.
- [ ] Align result bounding, runtime emission, worker capture, and wrapper parsing.
- [ ] Add exact-boundary and fail-closed host regressions.
- [ ] Run focused hook result and worker tests.

## Notes

- Verified from finding 99.
- Current `fmt.Fprintln` makes an accepted 8192-byte body consume 8193 captured bytes.

## Deviations
