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

- [x] Inventory frame ownership and every current parse/copy boundary
- [x] Extend strict validation to return bounded envelope metadata
- [x] Rewire observers and progress routing to the validated envelope
- [x] Define safe raw-slice lifetime and clone only data that escapes the frame
- [x] Reuse bounded reader/writer storage with retention and clearing rules
- [x] Avoid redundant post-transform validation when content is unchanged
- [x] Add fuzz, aliasing, pool-reuse, maximum-frame, and protocol tests
- [x] Add frame benchmarks and calibrated history
- [x] Update MCP framing and memory-bound documentation
- [x] Run race, fuzz, E2E, publication, and complete verification

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
- The strict Go 1.27 `jsontext` walk now validates grammar, UTF-8, duplicate
  names, root shape, depth, item, string, number, and trailing-data limits while
  retaining borrowed root `id`, `method`, `params`, `result`, and `error`
  slices. Protocol observers consume that one result; retained results,
  upstream correlation fields, and queued progress parameters clone only their
  escaping subslices.
- Original bytes continue directly to the SDK. Upstream correlation mutation
  is the only transform path; byte-identical output skips a second strict walk,
  while changed output is fully rescanned before delivery.
- Reader/writer buffers are cleared before reuse and retained only through
  256 KiB. Larger frame backing arrays become unreachable after the current
  frame, preventing a legal near-limit frame from permanently inflating the
  connection's retained heap.
- Calibrated Apple M1 medians at 100 fixed iterations: small frame 2,468 ns/op,
  744 B/op, 15 allocs/op; progress frame 4,379 ns/op, 928 B/op, 25 allocs/op;
  representative tool call 4,113 ns/op, 1,208 B/op, 32 allocs/op. Benchmark
  history format is now `reconc.performance-history/v6`.
- Verification passed the complete MCP race suite, 137,483 dedicated frame
  fuzz executions, alias/clearing/protocol regressions, calibrated benchmark
  record/baseline/compare, Vet, Staticcheck, and publication/reference checks.
  The cumulative complete race and release-trust gates remain part of the final
  TASK 283 verification.

## Deviations

None.
