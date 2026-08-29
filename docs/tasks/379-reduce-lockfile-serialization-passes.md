# TASK 379: Reduce lockfile serialization passes

## Why

Compile and runtime lockfile paths repeatedly marshal, decode, canonicalize, and indent the same payload. Near the 16 MiB lockfile ceiling this creates multiple full-size transient buffers and generic JSON trees.

## Acceptance

- Compile derives digest input and emitted lockfile bytes from one normalized representation with no redundant full encoding.
- Runtime validation retains canonical rules/actions for envelope validation and digest computation; no load-bearing normalization is removed.
- Canonical key ordering, numeric preservation, indentation, newline termination, digest identity, and old lockfile compatibility remain byte-stable.
- Maximum-size compile/load benchmarks prove lower peak allocations and total bytes allocated.

## Sub-Tasks

- [ ] Benchmark maximum-size compiler and runtime lockfile paths.
- [ ] Reuse canonical byte representations across digest and formatting stages.
- [ ] Remove or rename the pass-through `marshalCanonical` wrapper only after callers are migrated.
- [ ] Add byte-for-byte and malformed-envelope regressions; run focused benchmarks.

## Notes

- Verified from findings 10, 63, and 64 after correcting finding 10's original claim.
- `decodeCurrentLockfilePayload` canonicalization is not dead: `ValidateLockfileEnvelope` and `ComputeLockDigest` consume the normalized payload.
- The valid issue is multi-pass allocation cost, not removal of rules/actions canonicalization.

## Deviations
