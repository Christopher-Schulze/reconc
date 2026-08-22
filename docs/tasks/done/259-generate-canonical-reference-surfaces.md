# TASK 259: Generate canonical reference surfaces

## Why

Reconc already owns typed command metadata, hook/workflow registries, and a
versioned schema registry, but several human reference surfaces are still
maintained independently. Generating reviewable references from those owners
reduces drift without making runtime help, schemas, or release truth depend on
documentation parsing.

## Acceptance

- One deterministic generator emits command, hook/workflow, and schema
  reference sections directly from `commandmeta`, hook registries, and the
  schema contract registry.
- Generated content has explicit stable markers, deterministic ordering,
  bounded output, and a check mode that fails on drift without rewriting.
- Existing hand-written conceptual guidance remains hand-written; only exact
  inventories, synopses, outputs, event routes, compatibility state, and schema
  identities are generated.
- `make` exposes generation and drift-check targets, publication/release trust
  runs check mode, and CI cannot accept stale generated references.
- Help, completion, manpage, workflow parity, schema registry, and generated
  reference tests prove that every public canonical entry appears exactly once
  and internal surfaces stay hidden.
- Contributor and product documentation explains the source of truth and the
  regeneration command.

## Sub-Tasks

- [x] Define bounded deterministic reference projections and markers
- [x] Implement command, workflow, and schema reference generation
- [x] Add atomic generation and read-only drift checking
- [x] Wire Makefile, publication, and release-trust checks
- [x] Replace only duplicated exact inventories in documentation
- [x] Add parity, determinism, and stale-output regressions
- [x] Run focused and repository-wide verification
- [x] Archive the completed TASK and commit the verified change

## Notes

- `internal/commandmeta` already feeds root help, shell completions, and the
  manpage. `internal/hooks.VerificationSurfaces` and `internal/schema.Contracts`
  provide detached deterministic snapshots suitable for projection.
- Generated output must not become a second registry and must never include
  internal hook-runtime commands in the public command reference.
- `scripts/build/reference-docs` projects 103 public command nodes, 20 exact
  hook verification surfaces, and 36 schema contracts into three independently
  marked sections. Each projection has cardinality and byte bounds, duplicate
  detection, Markdown escaping, deterministic registry order, and a single
  canonical regeneration command.
- Generation preserves every byte outside the markers and atomically replaces
  only changed documents. `--check` compares in memory, reports every stale
  document, and never writes. Regression tests prove marker rejection,
  deterministic output, exact one-row ownership, internal-route exclusion,
  stale detection, and read-only check behavior.
- `make test-fast`, publication audit, release trust, and the release path all
  execute `reference-docs-check`. Focused Race tests cover the generator,
  command metadata, hook registry, schema registry, completion, and manpage;
  `make test-fast`, publication audit, focused `go vet`, formatting, shell
  syntax validation, and `git diff --check` pass.

## Deviations

None.
