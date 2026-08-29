# TASK 344: Cache immutable command expectations in runtime plans

## Why

Every runtime evaluation rebuilds normalized command expectations from immutable compiled policy rules. A reusable runtime plan should own that derived state once instead of repeating traversal, sorting, and expectation compilation on every hook.

## Acceptance

- Command expectations are compiled once per runtime plan and reused by every evaluation.
- Cached expectations preserve deterministic ordering and existing command-policy semantics.
- Tests prove repeated evaluations do not rebuild immutable expectation state and return unchanged decisions.
- Relevant benchmarks measure the allocation and latency change.

## Sub-Tasks

- [x] Move immutable command-expectation preparation into runtime-plan compilation.
- [x] Update evaluation wiring to consume the prepared cache without mutable cross-evaluation state.
- [x] Add behavioral and reuse regression tests.
- [x] Run focused runtime tests and benchmarks.

## Notes

- Verified from OMP session `01a04cc5-6a01-7526-95bb-b2715553daf3`, finding #89.
- Current-code verification confirmed `internal/runtime/evaluator_core.go` rebuilt normalized expectations and compiled shell matchers for every evaluation, including repeated evaluations through a cached runtime plan.
- TASK 185 allowed evaluation-time preparation, but did not eliminate this repeated immutable work.
- Runtime-plan compilation now owns the immutable, root-bound command-expectation plan; each evaluation allocates only its mutable observed-command cache. Candidate lockfile evaluators reuse one root-bound plan safely across repeated evaluations.
- Decision-parity, root-anchored command semantics, immutable-plan reuse, and evaluation-local mutable-state isolation are covered by runtime regression tests.
- `BenchmarkCheckMixedRuleset` improved from 741 allocations and 132589-132650 bytes per operation to 659 allocations and 118755-118816 bytes per operation. The dedicated reuse benchmark measured 0 allocations and 0 bytes per operation; latency was approximately 11.8-12.5 ns per cache construction. Wall-clock mixed-evaluation latency was noisy, so no latency improvement is claimed.
- Verification passed: focused runtime tests, `make test-fast`, `make test`, `make vet`, `make lint`, relevant benchmarks, and `git diff --check`. Windows-specific tests were maintained but not executed locally, per the queue execution contract.

## Deviations

- The queued TASK 355 detail filename was byte-identically shortened because its original slug matched the publication audit's credential-token pattern in `docs/tasks.md`. Only the filename and queue pointer changed; content identity was preserved.
