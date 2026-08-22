# TASK 244: Make evaluator caches and script decisions fail closed

## Why

Current evaluator hot paths contain two authorization false-positive risks and
three avoidable cache costs. Quote-blind redirect stripping can satisfy a
command-success requirement with a different command. Batch scripts can return
blocking exit status while empty JSON failures mark every rule handled and
passing. Pre-decision and evidence cache identities omit or over-sample state,
and the same hook payload is decoded repeatedly.

## Acceptance

- Trailing redirection removal uses the existing shell parser or an equivalent
  quote-aware token stream. Redirection operators inside quoted or escaped
  arguments are never removed.
- Batch `require_script` reconciles process disposition and structured output:
  blocking exit status can never become pass, missing modes fall back to
  per-rule execution, and malformed or contradictory output fails closed.
- The pre-decision key binds the exact repository Git alias state that shell
  authorization may read, or disables caching for alias-dependent decisions.
- `stopPolicyScanCache.stable` returns false for missing or unreadable lock
  identity regardless of caller-side `Cacheable` guards.
- Evidence identity uses stable platform-specific object identity fields and
  excludes access time and unrelated stat metadata.
- One bounded payload decode is passed through PreToolUse normalization, cache
  identity, cache path, and policy evaluation; pre/post cache sampling still
  detects concurrent state changes.
- Benchmarks demonstrate fewer payload decodes and evidence identity work
  without weakening source, policy, session, or taint revalidation.
- Unit, property, fuzz, race, and end-to-end hook tests cover quoting,
  contradictory batch output, alias mutation, unreadable locks, and metadata
  changes.

## Sub-Tasks

- [x] Reproduce the quoted-redirection false positive and contradictory batch result
- [x] Replace redirect tokenization with the canonical quote-aware shell path
- [x] Make batch process and JSON dispositions one fail-closed contract
- [x] Bind Git alias and unreadable-lock state into cache validity
- [x] Replace reflected stat identity with stable platform identities
- [x] Decode each PreToolUse payload once across cache and handler layers
- [x] Benchmark and run evaluator, agent-session, fuzz, race, and full gates
- [x] Update runtime cache and script-decision documentation

## Notes

- External findings: F-3, F-14, F-15, F-16, F-23, and F-27.
- Content hashing in source freshness is not part of this task. It remains the
  required defense against same-size and metadata-spoofed source changes.
- A quote-aware implementation should reuse `mvdan.cc/sh/v3`, already present
  in the module, unless the existing normalized command representation provides
  a smaller proven parser surface.
- `mvdan.cc/sh/v3` now owns trailing-redirection recognition. Fuzzing found and
  fixed the command-only edge cases `0>0` and `!>0`; quoted and escaped
  redirect-looking arguments remain intact.
- Batch results require complete, unique mode coverage and agreement between
  exit disposition and structured failures. Incomplete structured output
  executes each rule independently; contradictions block every batched rule.
- Command cache keys bind the effective Git alias configuration. PreToolUse
  decodes once and reuses the typed payload across cache and evaluation layers.
- Evidence object identity is device/inode on Unix and volume/file index on
  Windows. Access-time-only changes do not invalidate snapshots.
- Focused unit tests, the redirect idempotence fuzzer, focused race tests,
  Windows compile checks, `make test`, `make vet`, `make lint`, `make self-host`,
  module tidy drift, and diff checks passed. The payload-reuse benchmark removed
  29 allocations and about 2 KiB per key on the measured fixture.

## Deviations

None.
