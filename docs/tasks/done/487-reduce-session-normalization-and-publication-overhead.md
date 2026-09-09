# TASK 487: Reduce session normalization and publication overhead

## Why

Session mutation loads and normalizes state, applies a callback, normalizes again, performs a whole-state equality comparison and marshals the full indented document on changes. Bounded but growing sessions repeatedly pay sorting, cloning, size estimation and serialization costs.

## Acceptance

- Valid evidence admission avoids redundant whole-state normalization and serialization while preserving deterministic output and all capacity limits.
- Duplicate/no-op events do not publish a new state and incur less whole-state work than the baseline.
- Updates remain atomic, crash-safe and lossless under concurrent events; rotation, taint and replay protection are unchanged.
- Measured CPU/allocations or bytes written improve for representative growing sessions, with honest retained-memory and durability tradeoffs.

## Sub-Tasks

- [x] Profile duplicate reads, distinct writes, command results, overflow and rotation; count normalization, marshal and publication operations before edits.
- [x] Inventory mutation callbacks and introduce validated admission/change tracking at existing state boundaries rather than guessing changes from caller intent.
- [x] Normalize untrusted loaded state once, preserve deterministic normalized invariants through updates and use compact JSON for persisted state where compatible.
- [x] Maintain exact serialized-byte limits and existing atomic writes; adjust rotation calculations and legacy readers consistently.
- [x] Add equivalence, no-op, concurrent and crash/failure regressions; record benchmarks and run session/retention/race gates.

## Notes

### Review provenance

- Review finding 18 from the 2026-09-08 source review at `a60196dc2f0954fd4f09d252fa65c709edfe4b01`.
- Status: complete. The review established source-level evidence; measurements and regressions below are from this implementation.
- Dependencies: Depends on TASK 481 for replay-state representation. Coordinate shared evidence changes with TASK 486.

### Source anchors

- `internal/runtime/agentsession/state.go`
- `internal/runtime/agentsession/state_limits.go`
- `internal/runtime/agentsession/evidence_segments.go`
- `internal/runtime/agentsession/repository_run_hotpath_benchmark_test.go`

### Implementation findings

- `mutateSessionStateResolved` now skips collection work for exact no-ops and admits already-canonical updates through `sessionStateIsNormalized`; untrusted callback output still takes the defensive normalizer before publication.
- Bounded string mutators insert in deterministic order, so normal product updates retain canonical invariants without a second sort or rebuild. Command-result, pending-call, retired-key, epoch-map, overflow-marker and byte-counter checks remain explicit at the admission boundary.
- Session state publication remains newline-terminated and JSON-compatible, but uses compact deterministic JSON. Existing legacy readers continue to accept the format; evidence segments retain their existing format and limits.
- One full `make test` race run had a transient `internal/mcpgateway` receipt-replay `state_unavailable` failure. The isolated test passed without race and with `-race -count=5`; the complete `make test` retry passed both before and after the compact-JSON follow-up.

### Measurements

- Baseline before edits (`go test ./internal/runtime/agentsession -run '^$' -bench 'Benchmark(NormalizedMaximumStateMutationComparison|MaximumStateDeterministicPublication|RepositoryRunStopHotpath)' -benchtime=200ms -count=1`, Apple M1): `RepositoryRunStopHotpath` 15,674,232 ns/op, 327,166 B/op, 2,914 allocs/op; `NormalizedMaximumStateMutationComparison` 1,119,612 ns/op, 1,567,513 B/op, 8,263 allocs/op; normalized publication 854,525 ns/op, 517,994 B/op, 2,123 allocs/op; defensive renormalization 2,052,461 ns/op, 3,195,964 B/op, 4,078 allocs/op.
- Current representative run (`go test ./internal/runtime/agentsession -run '^$' -bench 'Benchmark(NormalizeSessionStateCanonicalAdmission|MaximumStateDeterministicPublication|DuplicateSessionMutation)' -benchtime=300ms -count=1`, Apple M1): canonical admission 468,159 ns/op, 280,780 B/op, 1,731 allocs/op; compact normalized publication 571,580 ns/op, 252,779 B/op, 2,121 allocs/op; defensive renormalization 1,219,028 ns/op, 1,441,473 B/op, 3,879 allocs/op; duplicate mutation 675,384 ns/op, 124,189 B/op, 1,093 allocs/op. The repository Stop end-to-end benchmark remains dominated by policy/evaluator work and was unchanged within run noise.

### Verification

- `make test-fast`: pass after clearing only build/test caches when a prior run exhausted the host's 179 MiB free space; final free space was 8.8 GiB before the full race gate.
- `make test`: pass after the compact-JSON change, including publication audit, uncached race suites, harness race suites and release trust.
- `make vet`, `make lint`, `make build VERSION=0.9.8`, targeted package tests, `go test -race ./internal/runtime/agentsession`, and `git diff --check`: pass. No version text or release tag was changed.

### Technical design

- Do not eliminate synchronization, fsync/atomic replacement or taint persistence to manufacture faster numbers.
- Admission is validated, not inferred from caller intent: product mutators preserve canonical bounded collections, while arbitrary callback output is checked and normalized before publication.
- Avoid unsafe aliasing between old/new state when replacing DeepEqual; shared mutable maps can hide changes.
- Do not introduce a database or a new append-log format unless profiling demonstrates the simpler changes are insufficient and a separately bounded migration is planned.

### Verification and completion

- Compare the semantic state of long real event sequences before/after optimization, including duplicate and reordered events.
- Verify exact capacity boundaries, UTF-8 encoded sizes, evidence rotation, legacy state and TASK 481 replay metadata.
- Measure small/near-limit active state and concurrent sessions; assert no extra publication for no-op events and no lost evidence.
- Before Done, run the affected package tests and required repository gates (`make test-fast`, `make test`, `make vet`, `make lint`, `git diff --check`); include reference/publication checks when their owned artifacts change. Record actual commands and outcomes in this file.
- Keep changes scoped, preserve unrelated work, flush current behavior into `docs/documentation.md` where needed, then archive this detail and create the single TASK commit. Never push or change the product version without explicit authorization.
- Never run repository-targeted Reconc commands against this product root; integration validation belongs in isolated temporary repositories and `make self-host`.

## Deviations

None.
