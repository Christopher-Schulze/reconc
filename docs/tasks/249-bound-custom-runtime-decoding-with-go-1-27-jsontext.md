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

- [ ] Inventory route pointer shapes and current decoder/error invariants
- [ ] Design one-pass pointer selection over Go 1.27 `jsontext.Decoder`
- [ ] Add explicit depth, cardinality, selected-byte, and retained-value budgets
- [ ] Preserve neutral request and canonical output compatibility
- [ ] Add differential, adversarial, fuzz, and maximum-boundary tests
- [ ] Benchmark allocations and latency against the current interface-tree decoder
- [ ] Run custom-runtime, race, fuzz, and full repository gates
- [ ] Update runtime and Go 1.27 documentation with measured results

## Notes

- External finding: F-66.
- This is the strongest verified additional Go 1.27 opportunity encountered by
  the audit. Generic methods, UUID, ML-DSA, and additional `CutLast` use do not
  improve this path.
- Do not replace one full tree with a custom second full tree. The acceptance
  condition is selective retention with explicit budgets.

## Deviations

None.
