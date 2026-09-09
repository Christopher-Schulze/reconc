# TASK 488: Reuse one inspection snapshot for session briefings

## Why

buildSessionBriefing independently validates sources, checks lock freshness, decodes lock summaries and assembles task/run/report views. Parts of discovery, parsing and board inspection are repeated even though the response is intentionally compact.

## Acceptance

- One briefing shares a validated operation-local source/lock/task snapshot wherever consumers need the same facts.
- Source parsing, lock decoding and full board inspection are not repeated unnecessarily; measured call counts demonstrate the reduction.
- Output remains current or explicitly uncertain under concurrent changes, side-effect-free and semantically identical to the corrected briefing contract.
- Small and large repository benchmarks demonstrate lower work/allocations without shifting cost to unbounded retained state.

## Sub-Tasks

- [x] Measure current discovery/load/parse/decode/task inspection counts for fresh, stale, missing and malformed inputs.
- [x] Read ValidatePolicyLockfileSnapshot and source-load context signatures; design a typed operation-local inspection result carrying facts and errors.
- [x] Pass shared source/lock/board facts into task, run and report renderers, retaining their independent diagnostic visibility.
- [x] Avoid decoding full generic lock summaries when typed already-validated counts are available.
- [x] Add output parity, concurrent-change and read-only tests; record before/after measurements and run CLI/runtime/task gates.

## Notes

### Review provenance

- Review finding 19 from the 2026-09-08 source review at `a60196dc2f0954fd4f09d252fa65c709edfe4b01`.
- Status: implemented and verified. The briefing now owns one operation-local snapshot; no process-global state is retained.
- Dependencies: Depends on TASK 476, TASK 477, TASK 478 and TASK 479.

### Source anchors

- `internal/cli/workflow_cmd.go`
- `internal/runtime/lockfile.go`
- `internal/runtime/runtime_plan.go`
- `internal/tasklifecycle/run_state.go`
- `internal/tasklifecycle/briefing.go`

### Technical design

- Use TASK 476 pure inspection and TASK 477 report classification; an optimization must not revive their stale or mutating behavior.
- Share an observed snapshot, not live mutable structs or a process-global briefing cache.
- Preserve the rule that a compact entry command does not run full audits or target commands.
- Render TASK 478/479 decisions from the shared facts; do not create a new competing action selector.

### Implementation and measurements

- `inspectSessionPolicy` creates one discovery-bound `SourceLoadContext`, loads and parses the source bundle once, computes the source digest once, and passes the typed bundle, parsed policy, and digest into `ValidatePolicyLockfileSnapshotSummaryWithSourceDigest`. The summary returns validated typed rule/source counts from the same lock decode, replacing the old second runtime source load plus generic lock summary decode.
- `addTaskBriefing` derives `RunState` once from its validated board, renders the briefing with `BuildBriefingWithRunState`, and passes that exact state to `ReadRepositoryRunStatusWithTaskState`. Active report inspection stays a separate bounded diagnostic because it reads a different persisted state boundary.
- Call-graph count for fresh, stale, missing-lock, and malformed-source cases: before = up to 2 discovery walks, 2 source loads/parses, 2 source-digest passes, 2 lock reads/decodes, and 2 TASK inspections; after = 1 of each operation when the corresponding stage is reachable. Missing discovery still skips policy loading; source and lock failures retain independent task/run/report diagnostics.
- Benchmark (`go test ./internal/cli -run '^$' -bench BenchmarkBuildSessionBriefing -benchmem -count=1`, Apple M1): small 1,681 iterations, 664,926 ns/op, 209,972 B/op, 2,175 allocs/op; large 283 iterations, 6,461,819 ns/op, 3,034,108 B/op, 41,062 allocs/op for 128 rules. The snapshot is local to the call and adds no retained cache.
- Validation: targeted CLI/runtime/task/agentsession tests passed; `make test-fast` passed; `make test` passed including publication audit, race suites, reference-doc checks, harness checks, and release-trust; `make vet` passed; `make lint` passed; `make build` passed; `git diff --check` passed.

### Verification and completion

- Cover no repository config, fresh lock, stale source, invalid lock, large task board, active task and historical report.
- Instrument or count real package operations to prove reuse rather than asserting internal implementation details alone.
- Compare exact machine output and bounded prose, filesystem immutability, elapsed time and allocations.
- Before Done, run the affected package tests and required repository gates (`make test-fast`, `make test`, `make vet`, `make lint`, `git diff --check`); include reference/publication checks when their owned artifacts change. Record actual commands and outcomes in this file.
- Keep changes scoped, preserve unrelated work, flush current behavior into `docs/documentation.md` where needed, then archive this detail and create the single TASK commit. Never push or change the product version without explicit authorization.
- Never run repository-targeted Reconc commands against this product root; integration validation belongs in isolated temporary repositories and `make self-host`.

## Deviations

None planned.
