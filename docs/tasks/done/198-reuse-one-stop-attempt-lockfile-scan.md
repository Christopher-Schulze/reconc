# TASK 198: Reuse one stop-attempt lockfile scan

## Why

Stop-policy capture calls `scanStopPolicyLockfile` from fingerprint,
cacheability, generation, and storage paths. A single stop attempt can therefore
read and decode the same bounded lockfile multiple times with the same write
paths.

## Acceptance

- One attempt-scoped immutable scan result is keyed by the exact lockfile
  content identity and normalized write-path identity.
- Fingerprinting, cacheability, generation capture, assurance/freshness inputs,
  and cache storage consume that same result.
- Pre/post stability checks detect lockfile or write-path changes and retry or
  fail closed rather than reuse a stale scan.
- No process-global cache retains unbounded rule data or crosses repository
  identity boundaries.
- Tests count physical scans, force mid-attempt mutations, and cover scan errors;
  benchmarks cover maximum lockfile size.

## Sub-Tasks

- [x] Define stop-attempt scan ownership and identity
- [x] Thread one scan result through all attempt phases
- [x] Preserve pre/post invalidation and error semantics
- [x] Add mutation, count, and benchmark tests
- [x] Run agentsession, runtime, race, and complete gates

## Notes

- Verified across callers of `scanStopPolicyLockfile` in
  `internal/runtime/agentsession/stop_cache.go`.

## Deviations

None.
