# TASK 313: Eliminate redundant compiler payload serialization

## Why

Compiler normalization performs marshal, generic decode with `UseNumber`, and canonical marshal over the same payload. Large action plans and lock payloads then pass through additional full serialization for validation and final output.

## Acceptance

- Benchmarks attribute every current compiler serialization pass before removal.
- Normalization preserves custom marshaler freezing, exact numbers, sorted keys, digest identity, and trailing-data rejection with fewer full-tree passes.
- Embedded action-plan equivalence remains independently validated without redundant byte reconstruction.
- Compiler, migration, schema, determinism, fuzz, and benchmark gates pass.

## Sub-Tasks

- [x] Record stage-level allocation and latency baselines
- [x] Design one canonical normalization pipeline
- [x] Reuse canonical bytes only across byte-identical contracts
- [x] Add maximum-lock and custom-marshaler regressions

## Notes

- Evidence: `internal/compiler/compiler.go:355-375,399-432,912-921`. Performance impact must be measured before claiming a speedup.
- Baseline on Apple M1, 256 rules/tools, 100 iterations: payload normalization 1.24 ms/approximately 755 KB/11,878 allocations; digest reconstruction 207 µs/approximately 60 KB/14 allocations; final encoding 308 µs/approximately 157 KB/13 allocations; expected-action normalization 655 µs/approximately 395 KB/6,478 allocations.
- Arbitrary custom marshalers require marshal, `UseNumber` decode, and sorted-key canonical marshal; optimization is limited to reusing those canonical bytes for identical digest input and comparing one normalized trusted typed action tree directly with the already-normalized lock tree.
- Direct typed action bytes are not byte-identical because struct field order differs from normalized map order; embedded actions therefore compare the normalized typed tree directly against the already-normalized lock tree, avoiding both false mismatch and redundant byte reconstruction.
- After change: canonical digest hashing is approximately 22 µs/128 B/2 allocations versus 207 µs/approximately 60 KB/14 allocations; expected-action normalization is approximately 539 µs/approximately 366 KB/6,463 allocations versus 655 µs/approximately 395 KB/6,478 allocations.
- Maximum lockfile boundaries, custom-marshaler single-freeze/sorted-key/exact-number/trailing-data cases, stable compilation, embedded actions, migration fuzz, compiler/runtime/schema suites, `make test`, `make vet`, Staticcheck v0.8.1, and `make self-host` passed.

## Deviations

None.
