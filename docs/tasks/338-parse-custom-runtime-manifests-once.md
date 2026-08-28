# TASK 338: Parse custom-runtime manifests once

## Why

Custom-runtime manifest decoding performs duplicate-key validation, generic field-presence decoding, nested route decoding, guarantee and field decoding, and final typed decoding as separate passes over the same bytes.

## Acceptance

- One strict bounded token/raw-message pass captures field presence and the data required for one typed decode.
- Duplicate keys, unknown fields, required fields, route guarantees, field matrices, size/depth/item bounds, and digest identity remain fail closed.
- No retained raw slice aliases mutable input after decode.
- Maximum-manifest allocation, fuzz, compatibility, and worker tests pass.

## Sub-Tasks

- [ ] Measure every current decode pass and allocation owner
- [ ] Design one strict manifest envelope decoder
- [ ] Remove nested generic re-decodes
- [ ] Add duplicate, missing, maximum, aliasing, and fuzz tests

## Notes

- Evidence: `internal/customruntime/decode.go:14-101`.

## Deviations

None.
