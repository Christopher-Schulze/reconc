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

- [ ] Reproduce the quoted-redirection false positive and contradictory batch result
- [ ] Replace redirect tokenization with the canonical quote-aware shell path
- [ ] Make batch process and JSON dispositions one fail-closed contract
- [ ] Bind Git alias and unreadable-lock state into cache validity
- [ ] Replace reflected stat identity with stable platform identities
- [ ] Decode each PreToolUse payload once across cache and handler layers
- [ ] Benchmark and run evaluator, agent-session, fuzz, race, and full gates
- [ ] Update runtime cache and script-decision documentation

## Notes

- External findings: F-3, F-14, F-15, F-16, F-23, and F-27.
- Content hashing in source freshness is not part of this task. It remains the
  required defense against same-size and metadata-spoofed source changes.
- A quote-aware implementation should reuse `mvdan.cc/sh/v3`, already present
  in the module, unless the existing normalized command representation provides
  a smaller proven parser surface.

## Deviations

None.
