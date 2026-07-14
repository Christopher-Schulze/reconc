# TASK 003: Stop-path performance and scope

## Why

Stop is the most latency-sensitive enforcement path. Standalone regressed from
Golem's single status snapshot and session-scoped write evaluation, while the
harness cache serializes independent cold audits and task-state fingerprints
scale with the entire archive.

## Acceptance

- One bounded Git status snapshot supplies tracked and untracked Stop fingerprint data.
- Stop evaluation scopes uncommitted paths to the current session's writes and fails closed when the scope cannot be proven.
- Clean reentrant and unchanged-evidence paths reuse only exact valid reports.
- Independent cold audit keys execute concurrently without duplicate publication or cache corruption.
- Task-state hot paths do not hash every archived TASK on every Stop.
- Hook latency budgets have benchmarks and regression tests with no weakened gates.

## Sub-Tasks

- [x] Port and harden the single-snapshot fingerprint contract.
- [x] Port and harden session-specific uncommitted-path scoping.
- [x] Replace the global cold-audit critical section with keyed coordination and atomic merge.
- [x] Make task-state fingerprints incremental and hot-set bounded.
- [x] Benchmark cold, warm, dirty, untracked, reentrant, and concurrent paths.

## Notes

Approved areas: 8 Stop fingerprint regression; 9 Session-specific stop scope;
11 Cache mutex serializes cold audits; 12 Task audit scaling.

Final proof on Apple M1: clean fingerprint median 14.97 ms; tracked-dirty
28.62 ms; untracked-directory 28.41 ms; cold Stop 15.79 ms; warm exact Stop
14.97 ms; clean reentrant Stop 14.85 ms; two independent cold cache keys
0.71 ms/pair. Root and nested-module tests, race tests, vet, builds, Unix tests,
Windows cross-compilation, cross-process same-key singleflight, and a real
two-process independent-key publication test pass.

## Deviations

None.
