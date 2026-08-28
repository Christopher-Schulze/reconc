# TASK 297: Bind runtime freshness metadata to the opened file

## Why

Runtime freshness records size, mode, modification time, and budget usage from a pre-open `Lstat`, while its digest and stable identity come from the later opened snapshot. Replacement between those operations can create one internally mixed observation.

## Acceptance

- Digest, size, mode, timestamps, identity, and aggregate-byte accounting derive from one opened stable snapshot.
- Path replacement before, during, and after open fails closed or yields a wholly coherent new observation.
- Existing same-size and restored-metadata mutation detection remains intact.
- Freshness, runtime-plan, race, and cross-platform tests pass.

## Sub-Tasks

- [x] Refactor freshness observation around the opened metadata
- [x] Reorder aggregate budget admission safely
- [x] Add replacement and budget-boundary regressions
- [x] Run runtime freshness and plan-cache gates

## Notes

- Evidence: `internal/runtime/source_freshness.go:285-330`.
- The bounded snapshot callback is the sole metadata authority. Aggregate-byte
  admission occurs against its opened size, but the shared counter advances
  only after the helper validates the same descriptor and path post-read.
- Verification: replacement at every open/read boundary, exact and rejected
  aggregate budgets, restored-metadata mutation detection, the complete runtime
  package and focused race tests, Windows amd64 test compilation and vet,
  `make test`, `make vet`, `make lint`, and `make self-host` all passed.

## Deviations

None.
