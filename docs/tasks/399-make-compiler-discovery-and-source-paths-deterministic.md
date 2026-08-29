# TASK 399: Make compiler discovery and source paths deterministic

## Why

`compileRepoPolicyWithDiscovery` can return `(nil, nil)` when a caller supplies a non-discovered result whose loader succeeds. `portableSourcePath` also delegates absolute-path semantics to the host OS, so the same lockfile path can be accepted on Windows and rejected on Unix.

## Acceptance

- Compiler entry points never return a nil compiled policy with a nil error.
- A loader/discovery contradiction returns a typed, actionable error without acquiring the compile lock.
- Portable source-path acceptance is host-independent for POSIX roots, Windows roots, separators, dot segments, and backslashes.
- Tests exercise hand-built discovery contradictions and a platform-neutral path matrix without requiring local Windows execution.

## Sub-Tasks

- [ ] Define the discovery/load invariant and error contract.
- [ ] Reuse the existing platform-independent rooted-path primitive where its exact semantics match.
- [ ] Add cross-platform table tests and caller regressions.
- [ ] Run focused compiler tests.

## Notes

- Verified from findings 60 and 61.
- Current production discovery loaders normally return an error, so the nil result is latent but still violates the helper's return contract.

## Deviations
