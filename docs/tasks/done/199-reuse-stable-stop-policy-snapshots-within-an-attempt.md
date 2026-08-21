# TASK 199: Reuse stable stop-policy snapshots within an attempt

## Why

Stop generation and stable-report logic recapture source digests, Git state,
task state, and report/evidence identities multiple times. Some duplication is
required for before/after stability, but current code also repeats captures
within the same phase rather than representing the phase snapshot explicitly.

## Acceptance

- The attempt has typed `before`, evaluation, and `after` snapshots with clearly
  owned Git, task, policy-source, session-evidence, and report identities.
- Each identity is captured once per required phase; equality checks compare
  complete snapshot fields rather than silently mixing capture times.
- Expensive Git/status/task/source operations do not occur while unrelated
  state locks are held unless required for atomicity.
- Cache hits and stores remain impossible when any required observation is
  unavailable, truncated, untrusted, or changed.
- Instrumented tests assert capture counts and mutation retries; benchmarks
  cover stable and dirty repositories.

## Sub-Tasks

- [x] Model stop-attempt phase snapshots and invariants
- [x] Consolidate duplicate captures within each phase
- [x] Preserve complete before/after stability checks
- [x] Add capture-count, mutation, contention, and benchmark tests
- [x] Run agentsession, race, and complete gates

## Notes

- Evidence spans `stop_generation.go`, `stop_cache.go`, and stable-report
  helpers in `internal/runtime/agentsession`.

## Deviations

None.
