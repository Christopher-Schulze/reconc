# TASK 318: Avoid repeated glob-expansion key construction

## Why

Glob alternative expansion repeatedly sorts override positions and formats integer fields into string keys for deduplication, cost accounting, and final ordering.

## Acceptance

- Each expansion derives or updates one deterministic comparable identity per mutation step.
- Deduplication, branch ordering, zero-length overrides, star shapes, segment resets, and admission limits remain exact.
- No unsafe hash-only equality can merge distinct expansions.
- Glob fuzz, maximum-alternative, determinism, and benchmark tests pass.

## Sub-Tasks

- [ ] Profile expansion-key construction by alternative count
- [ ] Design an allocation-minimal exact key representation
- [ ] Reuse keys across deduplication and ordering
- [ ] Run glob fuzz and compiler benchmarks

## Notes

- Evidence: `internal/action/glob.go:104-140,231-273`.

## Deviations

None.
