# TASK 190: Reuse one policy discovery and source-loading context

## Why

Policy loading performs overlapping discovery work: callers may discover the
repository before `LoadPolicySources`, the loader discovers it again, default
policy fragments are globbed in discovery and again during loading, and the
resolved repository root is recomputed for each source read. These operations
describe one logical source snapshot but are not carried through one typed
context.

## Acceptance

- A typed source-loading context carries the canonical root identity,
  discovery result, default fragment inventory, config identity, and snapshot
  validation data through one load.
- Default fragments are enumerated once; only additional validated includes
  require further expansion.
- Every reused path and root is revalidated at the security boundary so reuse
  cannot hide a mid-load identity change.
- Existing public entry points remain convenient and either construct or accept
  the context without parallel discovery implementations.
- Tests cover root/config/source changes between phases; benchmarks show fewer
  discovery, glob, and root-resolution operations.

## Sub-Tasks

- [x] Define the policy source-loading context and ownership
- [x] Thread discovery output into the loader and compiler/runtime callers
- [x] Reuse default fragment and root identity results safely
- [x] Add mutation, compatibility, and benchmark tests
- [x] Run ingest, compiler, runtime, and complete gates

## Notes

- `SourceLoadContext` binds one discovery result, canonical root identity,
  compiler-config identity, and per-default-glob fragment inventory. It is
  validated before and after loading; root, config, or default inventory drift
  fails closed.
- `LoadPolicySourcesWithContext` consumes the captured default matches and only
  expands additional validated include patterns. `CompileRepoPolicy` passes
  the same context through its compile-lock transaction; the legacy injected
  loader helper remains available for focused tests.
- Mutation/compatibility tests pass. Apple M1 benchmark with 16 fragments:
  context reuse 1.845 ms/op, 320,947 B/op, 3,097 allocs versus rediscovery
  2.344 ms/op, 356,146 B/op, 3,403 allocs.

## Deviations

None.
