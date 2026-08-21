# TASK 196: Normalize rule JSON once per compiler boundary

## Why

`normalizeJSONValue` marshals a Go value to JSON and decodes it with
`UseNumber`. Compiler flows invoke this conversion on rule payloads and again
for migrated action validation. This is a full serialization boundary, not a
cheap type conversion, and repeated calls duplicate allocations and validation
work.

## Acceptance

- Every logical rule/action payload crosses canonical JSON normalization at
  most once per compile phase and the normalized representation is reused by
  all consumers that require it.
- JSON number fidelity, custom marshal behavior, null/empty distinctions,
  trailing-value rejection, and unsupported-value errors remain unchanged.
- Typed paths bypass serialization only when equivalence is proven with tests;
  no `map[string]any` shortcut weakens validation.
- Golden and fuzz tests compare normalized values and encoded lockfile output.
- Benchmarks quantify the change on maximum-size rule sets.

## Sub-Tasks

- [x] Inventory normalization boundaries and representation owners
- [x] Establish one canonical normalized payload per owner
- [x] Remove duplicate marshal/decode cycles
- [x] Add semantic, fuzz, and benchmark coverage
- [x] Run compiler, action, and complete gates

## Notes

- `normalizeJSONValueWithBytes` now owns the canonical JSON boundary and
  returns both the decoded `UseNumber` tree and the validated canonical bytes.
  `normalizeJSONValue` remains a compatibility wrapper; action parity checks
  compare the returned bytes directly instead of re-marshaling the tree.
- Number fidelity and canonical-byte tests pass; compiler output and migration
  behavior remain unchanged. Apple M1 benchmark: one normalization 2.054
  us/op, 2,377 B/op, 42 allocs versus two boundaries 4.049 us/op, 4,754 B/op,
  84 allocs.

## Deviations

None.
