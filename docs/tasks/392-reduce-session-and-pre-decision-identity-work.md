# TASK 392: Reduce session and pre-decision identity work

## Why

Every state mutation normalizes the complete state before comparison and again during serialization. Pre-decision cache lookup repeatedly reloads and hashes policy sources, lockfile, session, taint, and alias state before and after reading the cache.

## Acceptance

- A changed session state is normalized exactly once before deterministic publication.
- No-op detection remains semantic and still repairs missing files or incorrect private modes.
- Pre-decision lookup reuses safe snapshots while retaining post-read identity resampling against every mutable input.
- Benchmarks prove lower allocations, filesystem reads, and hash bytes for maximum state and cache hits; deterministic mutation tests preserve fail-closed behavior.

## Sub-Tasks

- [ ] Benchmark maximum-state mutation and pre-decision hit/miss paths.
- [ ] Pass normalized state into deterministic marshaling without a second normalization.
- [ ] Reuse bound identity snapshots across cache phases without weakening revalidation.
- [ ] Run focused tests and benchmarks.

## Notes

- Verified from findings 43 and 44.
- The pre-decision resample is security-relevant and must remain; the target is redundant loading and materialization, not fewer integrity checks.

## Deviations
