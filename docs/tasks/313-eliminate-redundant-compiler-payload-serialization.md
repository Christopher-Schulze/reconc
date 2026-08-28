# TASK 313: Eliminate redundant compiler payload serialization

## Why

Compiler normalization performs marshal, generic decode with `UseNumber`, and canonical marshal over the same payload. Large action plans and lock payloads then pass through additional full serialization for validation and final output.

## Acceptance

- Benchmarks attribute every current compiler serialization pass before removal.
- Normalization preserves custom marshaler freezing, exact numbers, sorted keys, digest identity, and trailing-data rejection with fewer full-tree passes.
- Embedded action-plan equivalence remains independently validated without redundant byte reconstruction.
- Compiler, migration, schema, determinism, fuzz, and benchmark gates pass.

## Sub-Tasks

- [ ] Record stage-level allocation and latency baselines
- [ ] Design one canonical normalization pipeline
- [ ] Reuse canonical bytes only across byte-identical contracts
- [ ] Add maximum-lock and custom-marshaler regressions

## Notes

- Evidence: `internal/compiler/compiler.go:355-375,399-432,912-921`. Performance impact must be measured before claiming a speedup.

## Deviations

None.
