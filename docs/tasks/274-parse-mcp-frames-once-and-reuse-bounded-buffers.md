# TASK 274: Parse MCP frames once and reuse bounded buffers

## Why

Inbound MCP frames can be walked by strict JSON validation, unmarshaled again
by observers, cloned, unmarshaled again for progress routing, and finally parsed
by the SDK. `readFrame` also allocates a fresh 64 KiB backing slice for every
frame. Frames are bounded at 10 MiB, so repeated full passes and copies create
large transient allocation and CPU spikes.

## Acceptance

- One strict frame pass validates UTF-8, line framing, depth/item limits,
  duplicate object keys, trailing data, and root shape while extracting only
  the envelope metadata required by routing/observation.
- Observer, request/response bookkeeping, and progress routing consume the
  validated envelope/raw subslices without reparsing or cloning the complete
  frame. Ownership is explicit so no retained `RawMessage` aliases a reused
  buffer.
- The SDK still receives the exact original or intentionally transformed JSON
  bytes and remains the typed protocol authority.
- Reader and writer buffers are pooled or reused with a strict retention cap.
  Oversized frames are rejected without retaining a 10 MiB buffer indefinitely,
  and sensitive payload bytes are not exposed across calls.
- Transform paths perform one additional strict validation only when bytes were
  actually changed.
- Fuzz tests cover nested objects, duplicate keys, numeric/string progress
  tokens, partial reads, maximum frames, transformed frames, cancellation, and
  pooled-buffer reuse.
- Benchmarks record passes, bytes copied, B/op, allocs/op, and latency across
  small, representative, progress, and maximum frames.
- Protocol docs, race/fuzz tests, gateway E2E, and complete gates pass.

## Sub-Tasks

- [ ] Inventory frame ownership and every current parse/copy boundary
- [ ] Extend strict validation to return bounded envelope metadata
- [ ] Rewire observers and progress routing to the validated envelope
- [ ] Define safe raw-slice lifetime and clone only data that escapes the frame
- [ ] Reuse bounded reader/writer storage with retention and clearing rules
- [ ] Avoid redundant post-transform validation when content is unchanged
- [ ] Add fuzz, aliasing, pool-reuse, maximum-frame, and protocol tests
- [ ] Add frame benchmarks and calibrated history
- [ ] Update MCP framing and memory-bound documentation
- [ ] Run race, fuzz, E2E, publication, and complete verification

## Notes

- Current evidence: `strictFrameReader.Read` validates the frame, calls an
  observer that may unmarshal it, and may validate transformed bytes again;
  `sdkDownstream.routeProgress` performs envelope, params, and token unmarshals.
- Current evidence: `readFrame` starts with `make([]byte, 0, 64<<10)` for every
  frame and grows up to `MaxProtocolFrameBytes`.
- Full image decoding is explicitly not part of this task. Reconc's documented
  icon contract validates complete self-contained PNG/JPEG images and pixel
  budgets; dimensions-only parsing would weaken it.
- Do not replace the strict validator with `json.Valid`, which does not enforce
  the complete Reconc framing and cardinality contract.

## Deviations

None.
