# TASK 297: Bind runtime freshness metadata to the opened file

## Why

Runtime freshness records size, mode, modification time, and budget usage from a pre-open `Lstat`, while its digest and stable identity come from the later opened snapshot. Replacement between those operations can create one internally mixed observation.

## Acceptance

- Digest, size, mode, timestamps, identity, and aggregate-byte accounting derive from one opened stable snapshot.
- Path replacement before, during, and after open fails closed or yields a wholly coherent new observation.
- Existing same-size and restored-metadata mutation detection remains intact.
- Freshness, runtime-plan, race, and cross-platform tests pass.

## Sub-Tasks

- [ ] Refactor freshness observation around the opened metadata
- [ ] Reorder aggregate budget admission safely
- [ ] Add replacement and budget-boundary regressions
- [ ] Run runtime freshness and plan-cache gates

## Notes

- Evidence: `internal/runtime/source_freshness.go:285-330`.

## Deviations

None.
