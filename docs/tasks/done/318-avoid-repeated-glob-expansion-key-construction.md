# TASK 318: Avoid repeated glob-expansion key construction

## Why

Glob alternative expansion repeatedly sorts override positions and formats integer fields into string keys for deduplication, cost accounting, and final ordering.

## Acceptance

- Each expansion derives or updates one deterministic comparable identity per mutation step.
- Deduplication, branch ordering, zero-length overrides, star shapes, segment resets, and admission limits remain exact.
- No unsafe hash-only equality can merge distinct expansions.
- Glob fuzz, maximum-alternative, determinism, and benchmark tests pass.

## Sub-Tasks

- [x] Profile expansion-key construction by alternative count
- [x] Design an allocation-minimal exact key representation
- [x] Reuse keys across deduplication and ordering
- [x] Run glob fuzz and compiler benchmarks

## Notes

- Evidence: `internal/action/glob.go:50-140,149-188,239-280`.
- Each immutable expansion now retains one exact string identity, rebuilt only after a mutation step. Deduplication and final ordering compare that cached identity without repeating map scans, integer formatting, or sorting.
- Identity remains full-string and collision-free; no hash-only equality was introduced. Determinism tests verify cached keys match a fresh exact build, remain stable across equivalent runs, and are unique.
- Representative (12 alternatives) and maximum legal (1024 alternatives) expansion benchmarks measured 344 and 39,005 allocations per operation respectively, with the cached key work included once per expansion.
- Verification: glob contract/generated parity tests, identity/determinism regression, Glob fuzz (8s), glob benchmarks, action race tests, `make test-fast`, `make vet`, Staticcheck v0.8.1, and `make self-host` all passed.

## Deviations

None.
