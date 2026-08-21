# TASK 230: Centralize compiled-lockfile discovery state

## Why

The compiled lock path exists as mirrored constants and as another literal in
the publisher. After rendering, the compiler also removes the stale
lockfile-missing warning by substring-matching human text. These values agree
today, but path or wording changes can make publication, discovery metadata,
warnings, and returned `CompiledPolicy` disagree. The ingest/discovery owner
must expose one deterministic transition from pre-publication discovery to the
post-publication lockfile state.

## Acceptance

- Compiler metadata, filesystem publication, runtime reads, discovery, doctor,
  and CLI output derive the relative lockfile path from one canonical constant.
- Any retained exported compatibility constant is a compile-time alias of the
  canonical value, not a duplicated string.
- Discovery owns a pure copy-producing transition that marks the canonical
  lockfile as present and removes only the exact lockfile-missing condition.
- Compiler code never selects warning behavior with substring matching and an
  unrelated warning containing `lockfile not found` remains untouched.
- Pre-compile and post-compile `DiscoveryResult` values remain immutable to
  callers, deterministically ordered, and byte-compatible in lock payloads.
- Recompilation remains byte-identical and never retains the stale missing-lock
  warning after a successful atomic publication.
- Tests create missing, existing, multiply configured, and custom-warning
  discovery states and prove exact paths and warning preservation.
- Lockfile schema identities, format version, atomic/private publication, and
  repository boundary enforcement remain unchanged.

## Sub-Tasks

- [x] Establish one canonical lockfile path owner and compatibility alias
- [x] Add a pure typed post-publication discovery transition
- [x] Route compiler publication and metadata through the canonical state
- [x] Add warning-collision, immutability, and deterministic-recompile tests
- [x] Update architecture comments and run compiler/discovery/runtime gates

## Notes

- Session findings: `#3` and `#4`.
- Primary code: `internal/ingest/discovery.go` and
  `internal/compiler/compiler.go`.
- Warning output may remain `[]string` for schema compatibility. The internal
  transition still needs an exact owned identity, such as a discovery-owned
  constructor or condition code, rather than compiler knowledge of prose.
- This TASK does not change the public `.reconc/policy.lock.json` location.
- `internal/ingest.LockfilePath` is now the single path identity. The compiler's
  exported compatibility name is a compile-time alias; bootstrap,
  agent-session runtime, doctor/CLI, repository ignore output, and manpage
  rendering derive from the same constant.
- Discovery records an internal missing/present condition and owns the pure
  post-publication transition. The returned snapshot deep-copies all pointer
  and slice storage, removes only the exact discovery-owned missing warning,
  and cannot mutate the source bundle.
- Regression coverage proves missing, present, multiply configured, and
  warning-collision states, non-aliasing, stable recompilation, canonical
  metadata, and preservation of unrelated diagnostics.
- Verification: full tests for ingest, compiler, bootstrap, agent-session
  runtime, repository ignore, manpage, and CLI passed; focused ingest/compiler
  race tests and `go vet` across every touched package passed.

## Deviations

None.
