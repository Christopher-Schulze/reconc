# TASK 379: Reduce lockfile serialization passes

## Why

Compile and runtime lockfile paths repeatedly marshal, decode, canonicalize, and indent the same payload. Near the 16 MiB lockfile ceiling this creates multiple full-size transient buffers and generic JSON trees.

## Acceptance

- Compile derives digest input and emitted lockfile bytes from one normalized representation with no redundant full encoding.
- Runtime validation retains canonical rules/actions for envelope validation and digest computation; no load-bearing normalization is removed.
- Canonical key ordering, numeric preservation, indentation, newline termination, digest identity, and old lockfile compatibility remain byte-stable.
- Maximum-size compile/load benchmarks prove lower peak allocations and total bytes allocated.

## Sub-Tasks

- [x] Benchmark maximum-size compiler and runtime lockfile paths.
- [x] Reuse canonical byte representations across digest and formatting stages.
- [x] Remove or rename the pass-through `marshalCanonical` wrapper only after callers are migrated.
- [x] Add byte-for-byte and malformed-envelope regressions; run focused benchmarks.

## Notes

- Verified from findings 10, 63, and 64 after correcting finding 10's original claim.
- `decodeCurrentLockfilePayload` canonicalization is not dead: `ValidateLockfileEnvelope` and `ComputeLockDigest` consume the normalized payload.
- The valid issue is multi-pass allocation cost, not removal of rules/actions canonicalization.
- The maximum compiler A/B benchmark emits the same 15.5 MiB lockfile and reduced the stable allocation floor from 191,082,584 B/op to 130,021,672 B/op (31.9%) and approximately 4,760 allocations to 340 (92.9%).
- The maximum runtime A/B benchmark reduced the redundant typed-envelope stage from 49,104,960 B/op to 25,464 B/op (99.9%); the canonical rule/action bytes remain subject to their independent typed decoders.
- Byte equality covers exact numeric preservation above 2^53, HTML and string escaping, Unicode and escaped-key ordering, custom marshalers, indentation, newline termination, sorted digest insertion, and the exact 16 MiB boundary.
- Focused compiler/runtime tests, focused benchmarks, `make test-fast`, `gofmt`, and `git diff --check` passed on macOS arm64.

## Deviations
