# TASK 451: Keep hook liveness routes consistent

## Why

Route trimming can remove an entry from the bounded liveness map while leaving its fresh marker file, so the fast path suppresses re-recording for six hours. OMP user-python observations also rewrite the complete liveness document for every event.

## Acceptance

- A marker is considered current only when its exact route remains present and consistent in the bounded record.
- Trimming invalidates/removes stale route markers without deleting unrelated state.
- High-frequency observations update bounded route state without rewriting unrelated liveness data per event.
- Deterministic tests/benchmarks cover 33+ routes, marker races, trim/reinsert, concurrent observations, and crash recovery.

## Sub-Tasks

- [ ] Define one consistency invariant between route records and marker files.
- [ ] Make trim and fast-path validation maintain that invariant atomically.
- [ ] Introduce an incremental bounded observation path.
- [ ] Run focused liveness tests and benchmarks.

## Notes

- Verified from findings 192 and 203.
- Finding 203 applies specifically to OMP user-python observations, not every generic hook event.

## Deviations
