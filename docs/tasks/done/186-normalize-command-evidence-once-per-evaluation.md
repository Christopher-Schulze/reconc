# TASK 186: Normalize command evidence once per evaluation

## Why

Recorded commands, command results, and expected commands pass repeatedly
through `normalizeCommandSemantics` across require/forbid checks and composite
evaluation. Identical strings are trimmed, whitespace-scanned, split, and
classified once per rule instead of once per evaluation.

## Acceptance

- Evaluation builds one bounded immutable command-evidence index containing raw
  syntax, normalized semantics, result outcome, epoch/order, and any parse
  completeness needed by all consumers.
- Exact, prefix, shell-aware, redirection, and composite matching retain current
  behavior; raw syntax remains available where semantically required.
- Duplicate commands and results preserve ordering and evidence provenance.
- Benchmarks demonstrate reduced normalization work for many rules without
  regressing small evaluations.
- Tests cover whitespace, quoting, redirection, malformed syntax, duplicates,
  and command-result freshness.

## Sub-Tasks

- [x] Inventory every command-normalization consumer
- [x] Define one evaluation-scoped normalized command index
- [x] Migrate runtime and composite checks
- [x] Add semantic regression and benchmark coverage
- [x] Run runtime, shell-command, and complete gates

## Notes

- `commandEvidenceIndex` preserves ordered command/result records with raw
  syntax, normalized semantics, outcomes, and evidence epochs for the whole
  evaluation. `commandInvocationCache` supplies normalized expected forms and
  compiled shell expectations once per immutable plan evaluation.
- Runtime, composite, assurance, and trace matching consume the shared index;
  compatibility wrappers still build a local index for direct callers.
- Focused runtime/shell-command tests pass. Prepared matching benchmark:
  81.79 ns/op, 64 B/op, 2 allocs/op; reparsing baseline: 3.586 us/op,
  1,380 B/op, 35 allocs/op on Apple M1.

## Deviations

None.
