# TASK 257: Consolidate expensive test fixtures

## Why

The uncached race gate spends most of its wall time in bootstrap, CLI, and
agent-session packages. A source-first timing baseline confirmed repeated
advanced harness archive verification and hundreds of individually persisted
evidence mutations as avoidable setup work. Mutable repository identity,
transaction receipts, Git state, and live process environment must remain
isolated, so consolidation is limited to immutable preparation and equivalent
batched production mutations.

## Acceptance

- The embedded advanced harness archive is authenticated once per active
  product version, cached with a strict one-entry bound, and returned as a deep
  detached copy so callers cannot corrupt later loads.
- Concurrent and incompatible-version loads preserve exact validation and
  isolation semantics; invalid loads never replace a valid cache entry.
- Evidence-rotation tests use the minimum number of real state transactions
  needed to cross byte and item boundaries while still exercising production
  rotation, persistence, chain verification, tamper detection, and cleanup.
- Bootstrap, CLI, and agent-session behavior remains covered by real
  repositories and real generated artifacts. No shared mutable repository,
  fake receipt, skipped path, or always-green assertion is introduced.
- The same uncached package timing command records a before/after comparison,
  and focused race tests, formatting, vet, and the bounded fast suite pass.
- Contributor documentation explains which preparation may be shared and
  which identity-bearing fixtures must remain isolated.

## Sub-Tasks

- [x] Cache and detach the immutable embedded harness pack
- [x] Consolidate equivalent evidence-rotation setup transactions
- [x] Add cache-isolation and rotation-boundary regressions
- [x] Document fixture ownership and isolation rules
- [x] Measure the exact before/after package baseline and run verification
- [x] Archive the completed TASK and commit the verified change

## Notes

- Baseline command: `go test -p=1 -count=1 -json ./internal/bootstrap
  ./internal/cli ./internal/runtime/agentsession`.
- Baseline package elapsed times on Apple M1 were 78.396 s bootstrap, 78.691 s
  CLI, and 95.295 s agent session. The full race gate previously measured
  258.861 s, 240.300 s, and 153.822 s respectively.
- `initializeSyncFixture` is intentionally not shared because receipts and
  policy locks bind the canonical repository root and owned file identities.
- Offline hook verification already shares one disposable repository across
  all registered host kinds; its process environment makes parallel kind
  execution unsafe without a larger architectural redesign.
- The four changed evidence-rotation tests fell from 29.41 s total to 0.63 s
  under the same uncached serial timing command, saving 28.78 s per run. The
  whole-package comparison was intentionally not used as a speed claim because
  unchanged Git and hook tests varied materially between the two host runs.
- `go test -race -count=1 ./harness ./internal/runtime/agentsession`, the
  complete bounded `make test-fast` root/template gate, focused `go vet`, and a
  final detached-cache race rerun all passed.

## Deviations

None.
