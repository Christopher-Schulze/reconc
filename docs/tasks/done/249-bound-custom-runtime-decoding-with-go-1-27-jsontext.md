# TASK 249: Bound custom-runtime decoding with Go 1.27 jsontext

## Why

Custom runtime normalization accepts up to 64 MiB and decodes the complete host
document into `map[string]interface{}` even though a route selects only a small,
declared set of JSON pointers. The byte cap therefore permits large transient
object trees and garbage-collector pressure on the hook boundary. Go 1.27 now
provides the stable streaming `encoding/json/jsontext` API already used by
Reconc's lockfile and canonical action decoders.

## Acceptance

- Host payload validation is streaming and bounded by bytes, nesting depth,
  object members, array items, selected-field count, and retained selected bytes.
- Duplicate names, invalid UTF-8, invalid numbers, trailing data, and malformed
  pointer traversal remain rejected with the existing stable error classes.
- Only values reachable from declared route pointers are retained. Unselected
  subtrees are skipped with `jsontext.Decoder.SkipValue` and never materialized
  as interface trees.
- Multiple pointers sharing an ancestor are resolved in one pass without
  reparsing the entire payload for each pointer.
- The public neutral request and canonical JSON bytes remain byte-compatible for
  every currently valid fixture.
- The maximum accepted host payload is justified by measured retained memory;
  lower limits are applied where no real adapter fixture requires 64 MiB.
- Differential tests compare the streaming decoder with the current decoder on
  valid inputs, and fuzz tests cover duplicate keys, depth, cardinality,
  pointer overlap, unknown fields, and truncation.
- Benchmarks at small, typical, and maximum payload sizes show bounded peak
  allocation and no regression on typical hook payload latency.
- Custom-runtime and Go 1.27 documentation describes the streaming boundary and
  why no full generic host tree is retained.

## Sub-Tasks

- [x] Inventory route pointer shapes and current decoder/error invariants
- [x] Design one-pass pointer selection over Go 1.27 `jsontext.Decoder`
- [x] Add explicit depth, cardinality, selected-byte, and retained-value budgets
- [x] Preserve neutral request and canonical output compatibility
- [x] Add differential, adversarial, fuzz, and maximum-boundary tests
- [x] Benchmark allocations and latency against the current interface-tree decoder
- [x] Run custom-runtime, race, fuzz, and full repository gates
- [x] Update runtime and Go 1.27 documentation with measured results

## Notes

- External finding: F-66.
- This is the strongest verified additional Go 1.27 opportunity encountered by
  the audit. Generic methods, UUID, ML-DSA, and additional `CutLast` use do not
  improve this path.
- Do not replace one full tree with a custom second full tree. The acceptance
  condition is selective retention with explicit budgets.
- Shipped conformance host objects top out at 107 bytes. The host cap is reduced
  from 64 MiB to 8 MiB, aligned with other large policy/action inputs, while
  selected materialization is independently capped at 2 MiB.
- Apple M1, five iterations: 64 KiB streaming 301,792 ns/op and 284,515 B/op
  versus 592,175 ns/op and 412,702 B/op for the interface-tree reference;
  8 MiB streaming 14,545,458 ns/op and 33,584,300 B/op versus 29,269,683 ns/op
  and 50,358,718 B/op. The 256-byte fixed-cost path is slower, as documented.
- Verification: differential fixture and canonical-byte tests, adversarial
  syntax/budget tests, maximum-boundary tests, the 500-execution streaming
  parity fuzzer, package race tests, vet, staticcheck, publication audit,
  harness-pack check, root/template race suites, and release-trust fixtures all
  passed.

## Deviations

None.
