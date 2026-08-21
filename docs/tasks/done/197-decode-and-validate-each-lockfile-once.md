# TASK 197: Decode and validate each lockfile once

## Why

Runtime lockfile loading performs a strict token/key/depth scan, JSON decode,
migration, envelope validation, and later typed marshal/decode conversions.
Several of those passes independently traverse the same bounded lockfile. The
strict scan is security-relevant, so optimization must consolidate ownership
rather than remove protections casually.

## Acceptance

- One lockfile load pipeline produces a typed immutable envelope plus any
  canonical token metadata required for duplicate-key, Unicode, depth, number,
  and trailing-data validation.
- No downstream consumer re-marshals the complete rule set merely to regain a
  typed form already available at the boundary.
- Migration, strict validation, source precedence, action validation, and digest
  semantics remain identical and fail closed.
- Differential and fuzz tests compare accepted/rejected inputs and error classes
  against the current pipeline.
- Benchmarks prove reduced whole-lockfile passes and allocations.

## Sub-Tasks

- [x] Map every full lockfile traversal and its invariant
- [x] Design one strict typed decode pipeline
- [x] Remove redundant whole-payload conversions
- [x] Add differential, fuzz, migration, and benchmark tests
- [x] Run lockfile, runtime, compiler, and complete gates

## Notes

- Verified in `internal/runtime/lockfile.go` and runtime-plan construction.
- `TestDecodeStrictLockfileJSONMatchesTwoPassReference` and
  `FuzzDecodeStrictLockfileJSONParity` compare the single-pass decoder against
  an independent validate-then-decode oracle for duplicate keys, Unicode,
  depth, numbers, root shape, and trailing data.
- `TestDecodeLockfileCachesTypedPartsForCurrentAndMigratedLocks` proves current
  and migrated locks retain typed rule/action parts and compile identically to
  the fallback payload path. `BenchmarkLoadLockfile` measures the complete
  bounded load path.
- A cache hash is not an integrity substitute and must not replace strict input
  validation.

## Deviations

None.
