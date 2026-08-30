# TASK 399: Make compiler discovery and source paths deterministic

## Why

`compileRepoPolicyWithDiscovery` can return `(nil, nil)` when a caller supplies a non-discovered result whose loader succeeds. `portableSourcePath` also delegates absolute-path semantics to the host OS, so the same lockfile path can be accepted on Windows and rejected on Unix.

## Acceptance

- Compiler entry points never return a nil compiled policy with a nil error.
- A loader/discovery contradiction returns a typed, actionable error without acquiring the compile lock.
- Portable source-path acceptance is host-independent for POSIX roots, Windows roots, separators, dot segments, and backslashes.
- Tests exercise hand-built discovery contradictions and a platform-neutral path matrix without requiring local Windows execution.

## Sub-Tasks

- [x] Define the discovery/load invariant and error contract.
- [x] Reuse the existing platform-independent rooted-path primitive where its exact semantics match.
- [x] Add cross-platform table tests and caller regressions.
- [x] Run focused compiler tests.

## Notes

- Verified from findings 60 and 61.
- Current production discovery loaders normally return an error, so the nil result is latent but still violates the helper's return contract.
- A hand-built undiscovered result whose loader succeeded reproduced the `(nil, nil)` return before the fix. The compiler now returns an actionable `PolicySourceError` on that contradiction before creating `.reconc` or acquiring the publication lock.
- Source provenance now reuses `pathidentity.Rooted` and `pathidentity.EscapesLexically`, rejects backslashes and non-canonical slash paths, and admits only exact host-independent logical identities.
- Cross-platform path matrices cover POSIX, drive, drive-relative, UNC, backslash, whitespace, dot-segment, repeated-separator, repository-relative, preset, and global identities. The current lock-envelope caller rejects the same unsafe forms.
- Focused compiler regressions and the full `internal/compiler` package passed.
- `make test-fast` passed.

## Deviations
