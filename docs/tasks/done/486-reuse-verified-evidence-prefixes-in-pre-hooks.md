# TASK 486: Reuse verified evidence prefixes in pre-hooks

## Why

Command and write pre-hooks call loadCompleteSessionEvidence without a cache, rereading and decoding sealed history on each event. Stop handling already owns a bounded verified-prefix cache. Long sessions therefore repeat historical work at a frequent prevention boundary.

## Acceptance

- Lifecycled pre-hook workers reuse already verified immutable evidence prefixes without repeatedly decoding and merging unchanged segments.
- Deleted, corrupted or replaced segments, chain gaps and generation changes still invalidate the prefix and fail closed where required.
- Cache memory has a byte bound and is isolated by repository/session/generation; one-shot CLI behavior remains correct without a warm cache.
- Long-session benchmarks show reduced historical decoding/allocation work while cold/error paths preserve behavior.

## Sub-Tasks

- [x] Trace existing StopDecisionCache prefix ownership, validity checks and all pre-hook worker entry points; record segment read/decode/merge counts.
- [x] Thread the existing verified-prefix cache or its narrowly extracted owner through pre-hook evaluation without creating another independent cache.
- [x] Preserve TASK 474 dependency binding and current generation/chain checks, including mutation between verification and decision publication.
- [x] Add warm-prefix, append-only suffix, corruption and multi-session regression coverage.
- [x] Measure 0/1/many/max-segment sessions, update cache lifecycle documentation and run session/worker race and full gates.

## Notes

### Review provenance

- Review finding 17 from the 2026-09-08 source review at `a60196dc2f0954fd4f09d252fa65c709edfe4b01`.
- Status: implemented. The review established source-level evidence; this TASK reproduces the behavior with worker-path regressions and bounded benchmark evidence.
- Dependencies: Depends on TASK 474. Coordinate byte accounting with TASK 489; do not require its broader cache changes for prefix correctness.

### Source anchors

- `internal/runtime/agentsession/handlers.go`
- `internal/runtime/agentsession/evidence_segments.go`
- `internal/runtime/agentsession/evidence_segments_test.go`
- `internal/runtime/agentsession/pre_decision_cache.go`

### Technical design

- Reuse immutable sealed history only after its current observations match; active evidence is still loaded from its present generation.
- Maintain existing byte budgets and eviction semantics; do not pin every session's entire history in the worker.
- Avoid exposing mutable cached slices/maps to evaluation or mutation callers.
- Treat a corrupt prefix as an integrity failure, not an opportunity to rebuild a truncated history and allow the action.

### Verification and completion

- Run repeated real pre-command and pre-write events with unchanged history and assert reduced decode/merge counts.
- Append a valid segment, mutate or delete an old segment and replace a segment identity; verify correct reuse or rejection.
- Exercise concurrent sessions/roots and worker shutdown under the race detector; report allocation and memory bounds.
- Before Done, run the affected package tests and required repository gates (`make test-fast`, `make test`, `make vet`, `make lint`, `git diff --check`); include reference/publication checks when their owned artifacts change. Record actual commands and outcomes in this file.
- Keep changes scoped, preserve unrelated work, flush current behavior into `docs/documentation.md` where needed, then archive this detail and create the single TASK commit. Never push or change the product version without explicit authorization.
- Never run repository-targeted Reconc commands against this product root; integration validation belongs in isolated temporary repositories and `make self-host`.

### Implementation evidence

- `RunHookRequestWithEvaluatorAndStopCache` passes its one `StopDecisionCache`
  through pre-decision identity sampling, live command/write enforcement,
  classified/fallback MCP pre-hooks, and Antigravity pre-tool enforcement.
  One-shot wrappers pass `nil` and retain cold loading. Existing segment identity,
  generation, chain-head, and revalidation checks remain the cache boundary,
  with the existing 64-entry / 16 MiB bounds.
- `TestWorkerPreDecisionHooksReuseVerifiedEvidencePrefix` covers two-segment
  cold/warm reuse, append-only suffix extension, a second session's isolated
  prefix, and corruption eviction with fail-closed rejection. Existing
  evidence-segment tests retain replacement, deletion, same-size mutation,
  generation fallback, 64-segment, and aggregate-budget coverage.

### Verification and measurements

- Local Apple M1 benchmark (`go test ./internal/runtime/agentsession -run '^$'
  -bench BenchmarkWorkerPreHookVerifiedEvidencePrefix -benchtime=100ms
  -count=1`): cold `2,546,911 ns/op`, `729,087 B/op`, `6,164 allocs/op`;
  warm `2,097,318 ns/op`, `646,082 B/op`, `5,592 allocs/op`.
- `go test -race ./internal/runtime/agentsession` passed.
- `make test-fast` passed.
- `make test` passed: publication audit, uncached `-race ./...`, portable-template
  race suite, release-trust, and reference/publication checks.
- `make vet`, `make lint`, `make self-host`, and `git diff --check` passed after
  the final code and documentation changes.

## Deviations

None.
