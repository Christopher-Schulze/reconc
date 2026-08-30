# TASK 451: Keep hook liveness routes consistent

## Why

Route trimming can remove an entry from the bounded liveness map while leaving its fresh marker file, so the fast path suppresses re-recording for six hours. OMP user-python observations also rewrite the complete liveness document for every event.

## Acceptance

- A marker is considered current only when its exact route remains present and consistent in the bounded record.
- Trimming invalidates/removes stale route markers without deleting unrelated state.
- High-frequency observations update bounded route state without rewriting unrelated liveness data per event.
- Deterministic tests/benchmarks cover 33+ routes, marker races, trim/reinsert, concurrent observations, and crash recovery.

## Sub-Tasks

- [x] Define one consistency invariant between route records and marker files.
- [x] Make trim and fast-path validation maintain that invariant atomically.
- [x] Introduce an incremental bounded observation path.
- [x] Run focused liveness tests and benchmarks.

## Notes

- Verified from findings 192 and 203.
- Finding 203 applies specifically to OMP user-python observations, not every generic hook event.
- Markers now bind the exact route timestamp and stable liveness-file generation. Any main-state rewrite invalidates old fast-path markers; a retained route rebuilds only its marker under the shared lock.
- Observation sidecars bind the current route occurrence, accept one previous route timestamp during crash-safe refresh, and are merged only while that route remains in the bounded main record. Trim removes only artifacts for evicted routes.
- Focused liveness and OMP observation tests passed. The final 100 ms local benchmark measured incremental updates at 9.78 ms/op, 120,706 B/op, and 1,438 allocs/op versus forced route refresh at 34.00 ms/op, 162,469 B/op, and 1,981 allocs/op.

## Deviations

- Per user direction, full module, race, vet, lint, release, and platform gates are deferred until TASK 460 so they run once over the final queue state. This TASK used only focused package tests and short benchmarks.
