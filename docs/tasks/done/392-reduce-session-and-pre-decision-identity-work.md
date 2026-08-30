# TASK 392: Reduce session and pre-decision identity work

## Why

Every state mutation normalizes the complete state before comparison and again during serialization. Pre-decision cache lookup repeatedly reloads and hashes policy sources, lockfile, session, taint, and alias state before and after reading the cache.

## Acceptance

- A changed session state is normalized exactly once before deterministic publication.
- No-op detection remains semantic and still repairs missing files or incorrect private modes.
- Pre-decision lookup reuses safe snapshots while retaining post-read identity resampling against every mutable input.
- Benchmarks prove lower allocations, filesystem reads, and hash bytes for maximum state and cache hits; deterministic mutation tests preserve fail-closed behavior.

## Sub-Tasks

- [x] Benchmark maximum-state mutation and pre-decision hit/miss paths.
- [x] Pass normalized state into deterministic marshaling without a second normalization.
- [x] Reuse bound identity snapshots across cache phases without weakening revalidation.
- [x] Run focused tests and benchmarks.

## Notes

- Verified from findings 43 and 44.
- The pre-decision resample is security-relevant and must remain; the target is redundant loading and materialization, not fewer integrity checks.
- Cache candidates are now read before one complete lookup-identity sample. Misses still resample every mutable component after evaluation before warming the cache.
- Maximum-state deterministic publication improved from 2,144,266 ns/op, 3,193,363 B/op, and 4,079 allocs/op to 892,644 ns/op, 513,660 B/op, and 2,123 allocs/op in the focused benchmark.
- Pre-decision hits improved from 820,058 ns/op, 129,800 B/op, 1,032 allocs/op, and two complete identity samples to 418,254 ns/op, 66,709 B/op, 530 allocs/op, and one post-cache-read identity sample.

## Deviations
