# TASK 338: Parse custom-runtime manifests once

## Why

Custom-runtime manifest decoding performs duplicate-key validation, generic field-presence decoding, nested route decoding, guarantee and field decoding, and final typed decoding as separate passes over the same bytes.

## Acceptance

- One strict bounded token/raw-message pass captures field presence and the data required for one typed decode.
- Duplicate keys, unknown fields, required fields, route guarantees, field matrices, size/depth/item bounds, and digest identity remain fail closed.
- No retained raw slice aliases mutable input after decode.
- Maximum-manifest allocation, fuzz, compatibility, and worker tests pass.

## Sub-Tasks

- [x] Measure every current decode pass and allocation owner
- [x] Design one strict manifest envelope decoder
- [x] Remove nested generic re-decodes
- [x] Add duplicate, missing, maximum, aliasing, and fuzz tests

## Notes

- Evidence: `internal/customruntime/decode.go:14-101`.
- Manifest shape admission now uses one bounded token pass that checks duplicate keys, required fields, allowed nested fields, object/array shape, and depth before the single typed `Manifest` decode.
- The scanner consumes unknown values to retain depth and duplicate-key precedence, while typed decoding remains the authoritative scalar and compatibility validation boundary. Decoded manifests retain no input-byte aliases.
- Regressions cover nested duplicate keys and mutable-input aliasing; existing maximum, missing-field, unknown-field, malformed, compatibility, and fuzz coverage remains green. Validation: `go test ./internal/customruntime -count=1`, `go test ./... -run '^$'`.

## Deviations

None.
