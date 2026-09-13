# TASK 554: Optimize measured agent hot paths

## Why

TASK 539's 24 benchmark targets passed their budgets, but its evidence is not a fresh performance measurement of the final integration changes. Allocations, peak memory, repeated policy identity work, and tail latency need a bounded Pareto pass without weakening cache correctness.

## Acceptance

- Fresh paired measurements identify exact source revisions, environment, workload, raw latency distributions, normalized comparisons, allocations, bytes, and peak-memory evidence.
- Implemented optimizations address measured dominant costs and show a repeatable material improvement on the selected workload with no correctness or suite-budget regression.
- All current 24 benchmark targets remain represented and pass their existing budgets; new DSH/integration targets are added only for genuinely new hot paths.
- Cache dependency identity, concurrent mutation detection, cancellation generation, and bounded evidence handling retain their existing adversarial tests.
- The report distinguishes median, p95, allocations, and RSS rather than calling one favorable metric universal optimization.
- No baseline refresh, weakened budget, or omitted failing target hides a regression.

## Sub-Tasks

- [x] Record a clean post-integration baseline and bounded CPU/allocation/IO profiles.
- [x] Select at most three dominant cost centers and document expected gain and invariants.
- [x] Implement and verify one measured change at a time.
- [x] Run paired comparisons and the complete benchmark inventory; report the Pareto stopping decision.

## Technical Plan

1. Reuse `scripts/benchmarks/history`, its record/record-pair/compare commands, `make benchmark-profile`, and existing session/hook-worker benchmarks. Read their actual flags before invocation. Store reproducible artifacts under `.build/benchmarks/`; do not write a second benchmark framework.
2. Compare clean source snapshots with the same Go version, dependency graph, hardware, power/thermal conditions, workload, and bounded concurrency. Keep cold and warm paths separate. Perform profiling in a separate diagnostic run so profiling overhead is not presented as benchmark timing.
3. Initial source candidates are `internal/runtime/agentsession/pre_decision_cache.go` identity serialization/hash work, dependency/path sampling, `internal/cli/hook_worker.go` transport/lifecycle, and session briefing assembly. These are hypotheses until a profile identifies dominant cost. Inspect actual callers and reuse existing identity/bounded-IO utilities before changing them.
4. Prefer eliminating repeated serialization, copies, normalization, or reads within an already validated immutable snapshot. Consider bounded buffer reuse only with proven lifetime isolation. Never reuse dependency observations across the required pre/post identity barriers or weaken candidate/evidence identity to TTL, mtime-only, or pointer-only checks.
5. Select no more than three cost centers from the profiles. Target a repeatable double-digit relative improvement in a selected dominant allocation/time metric or a material absolute user-flow saving. Treat that as a selection threshold, not a promise to alter code until an arbitrary percentage appears.
6. Stop investigating a candidate after two controlled variants fail to beat noise or when the next improvement requires substantial architectural complexity for marginal benefit. Record rejected candidates and measured headroom. Do not introduce SIMD, unsafe pooling, PGO build requirements, new runtimes, or dependency churn without an independently demonstrated high-return need.
7. Run the complete 24-target inventory once after the selected fixes, widening only if results reveal unresolved regressions. Include cold/warm raw p95 and peak-memory observations next to medians and normalized ratios. Retain the baseline rather than refreshing it to bless results.

## Verification

Run targeted `go test -race ./internal/runtime/agentsession ./internal/cli` and existing dependency/concurrency/cancellation tests after each logical optimization. Use existing paired-record/compare controls and `make benchmark-record` plus `make benchmark-compare` for the final suite with explicit artifact destinations. Run `make test`, `make vet`, and `make lint` before closure. Do not infer improvement from allocation counts alone.

## Dependencies

TASKS 544-553, so measurements include the completed integration and agent-output paths.

## Notes

Retained TASK 539 measurements include roughly 665,122 B/op and 5,800 allocations on the warm session-pre target, and 747,158 B/op and 6,367 allocations cold. Raw tail timings were not uniformly better than the old baseline despite passing calibrated budgets. These are historical comparison points only; the planning pass had not run a full fresh benchmark.

Fresh post-integration baseline: clean commit `942d01683a6b9f9760184af854725beff4dd654a`, Go 1.27.1, darwin/arm64 Apple M1, 5 samples x 3 repetitions x 250 ms, 19 groups and all 24 targets, recorded under `.build/benchmarks/task554-post-integration.json`. A separate bounded diagnostic record and CPU/heap/block/mutex/trace profiles for `session-evidence-workloads` and `runtime-source-freshness` used 1 sample x 1 repetition x 100 ms and live under `.build/profiles/task554/`; their timing is not a baseline. The fresh cold pre-hook target measured median 2,688,335 ns/op, 747,124 B/op, 6,368 allocs/op; the large source freshness target measured 3,696,132 ns/op, 659,752 B/op, 4,660 allocs/op. Heap profiles attribute 37% of source-freshness diagnostic allocation space to `observeFreshnessFilesWithContext` and 20% to `os.lstatNolog`; CPU profiles are dominated by filesystem syscalls, so removing only small Go allocations has limited user-visible value.

The checked baseline is result format v4 and cannot be compared directly with the current v6 recorder. `make benchmark-compare` correctly reported an incompatible measurement format; it did not report a pass or regression for 24 targets. The checked baseline remains unchanged. A separate, ignored v6 reference at `.build/benchmarks/task554-reference-v6.json` binds the freshly measured clean commit and uses the same seven tolerance values as the checked baseline for the paired comparison.

Selected cost center 1: `observeFreshnessFileSeeded` performs an existence `os.Lstat` before `boundedio.WithRegularFileSnapshot`, whose protected open already performs a second initial `os.Lstat`. An optional snapshot entrypoint could expose only the first-check absence while retaining the existing before/open/after identity barriers. Expected gain is one `lstat` and its metadata allocation per present file; no hash, path, directory, byte budget, or revalidation barrier may be skipped. Existing replacement-before-open tests require accepting one coherent replacement, while replacement-after-open and disappearance during open must still fail closed. Measure a first variant against the clean source before selecting a second cost center.

First candidate implemented and race-tested. A complete interleaved `record-pair` compared clean commit `942d01683a6b9f9760184af854725beff4dd654a` with isolated candidate `ba57990203d4324144521ad90cf4d84d0f2bba7f` on the same Apple M1 and Go 1.27.1, with 5 samples x 3 repetitions x 250 ms. All 24 targets passed the unchanged v6-reference budgets, with zero reported regressions. Large source freshness improved median 3,675,709 to 3,443,090 ns/op (-6.3% raw, -4.6% normalized), p95 3,680,651 to 3,460,188 ns/op, allocation 659,752 to 616,792 B/op (-6.5%), 4,660 to 4,550 allocs/op (-2.4%), and peak RSS 67,944,448 to 64,880,640 bytes (-4.5%). Warm pre-hook latency was flat at 2,292,759 to 2,290,891 ns/op, with allocations increasing 5,800 to 5,869/op (+1.2%). The candidate therefore helps source freshness but does not materially optimize the more frequent warm pre-hook flow by itself.

Selected cost center 2: `evidenceSnapshot.clone()` in the verified evidence-prefix cache calls `apply()` then `snapshotEvidence()`, copying each retained slice and map twice on both cache lookup and storage. A direct fieldwise clone should preserve owned copies and command-result depth while eliminating one entire copy pass. Expected gain was lower warm pre-hook allocation and heap traffic; cached prefixes had to remain isolated from caller mutation, and segment identity/content revalidation had to remain unchanged.

The second complete paired run used isolated candidate `29f36043e816530dc6c713de2934091b7c399006` against the same clean baseline commit, parameters, host, and unchanged tolerances. All 24 targets passed with zero regressions. Warm pre-hook median changed 2,290,543 to 2,280,976 ns/op (-0.4% raw), while allocations changed 5,800 to 5,861/op (+1.1%); this does not establish an end-to-end gain from the snapshot-copy change. The clone edit and its test were reverted from the product tree. The profile's large `preDecisionSessionDependenciesWithStopCache` cost includes required session/evidence pre/post identity barriers and filesystem calls; removing those would weaken cache correctness.

Selected cost center 3: `observeFreshnessFilesWithContext` eagerly allocates a 32 KiB read buffer. A lazy allocation would save one allocation on a fully seeded pass, while leaving digest, file identity, directory, and cancellation checks intact. Focused large-source-hit measurements after the lazy-allocation edit were 3,269,775/3,259,565/3,288,246 ns/op, 616,803/616,797/616,799 B/op, and 4,550 allocs/op. These are the same allocation and byte levels as the first candidate's complete paired result because this workload still has a file that needs hashing. The edit was reverted without spending another full paired run on a zero-gain variant.

The product-tree code now exactly matches the first measured candidate across all changed production and test files. Raw five-sample distributions (baseline -> candidate) for the selected large-source target were ns/op `[3669291, 3670904, 3677047, 3681552, 3675709]` -> `[3462797, 3449753, 3438593, 3441894, 3443090]`, B/op `[659752, 659752, 659752, 659752, 659752]` -> `[616792, 616776, 616792, 616792, 616792]`, and allocations/op `[4660, 4660, 4660, 4660, 4660]` -> `[4550, 4550, 4550, 4550, 4550]`. Warm pre-hook ns/op were `[2296638, 2292759, 2291050, 2289549, 2295112]` -> `[2288295, 2293991, 2290891, 2295710, 2289110]`; cold ns/op were `[2676354, 2667172, 2688095, 2668814, 2678941]` -> `[2681195, 2681604, 2675156, 2675218, 2674965]`. Large-source peak-RSS samples were `[64585728, 64487424, 67944448, 65208320, 64454656]` -> `[64536576, 64651264, 63963136, 59768832, 64880640]` bytes. RSS varies more than the median latency; do not claim a universal memory win.

Complete first-pair inventory, 5 samples x 3 repetitions x 250 ms, baseline `942d01683a6b9f9760184af854725beff4dd654a`, candidate `ba57990203d4324144521ad90cf4d84d0f2bba7f`, 24/24 target budgets pass (raw median changes; all seven existing normalized/absolute budgets checked per target):

| Group | Target | Raw time | Bytes | Allocations |
| --- | --- | ---: | ---: | ---: |
| action-bounded-trace | `BenchmarkActionEvaluatorMaximumLegalPlanCalibrated` | +0.4% | 0.0% | 0.0% |
| action-bounded-trace | `BenchmarkActionContextRootPredicates` | +1.3% | 0.0% | 0.0% |
| action-decision-cache | `BenchmarkPreparedDecisionCacheStore` | -0.5% | 0.0% | 0.0% |
| action-ledger-checkpoint | `BenchmarkLedgerCheckpointAdvanceActive256` | +1.0% | 0.0% | 0.0% |
| action-ledger-checkpoint | `BenchmarkLedgerCheckpointAdvanceTerminal65536` | +0.2% | 0.0% | 0.0% |
| action-structured-inspection | `BenchmarkMaximumLegalContentArray` | +0.6% | 0.0% | 0.0% |
| compiler-canonical-json | `BenchmarkNormalizeJSONValueOnce` | +0.2% | 0.0% | 0.0% |
| compiler-conflict-scaling | `BenchmarkDetectConflictsGroupedDuplicates` | +0.4% | 0.0% | 0.0% |
| hook-worker-frame-growth | `BenchmarkHookWorkerFrameLarge` | -0.1% | 0.0% | 0.0% |
| hook-worker-end-to-end | `BenchmarkHookRuntimeTransport/one-shot` | +0.3% | 0.0% | 0.0% |
| ingest-source-context | `BenchmarkLoadPolicySourcesWithContext` | -0.3% | 0.0% | 0.0% |
| mcp-frame-routing | `BenchmarkParseFrameProgress` | -0.3% | 0.0% | 0.0% |
| mcp-frame-routing | `BenchmarkParseFrameRepresentative` | -0.1% | 0.0% | 0.0% |
| prospective-path-resolution | `BenchmarkResolveProspectiveBatch` | 0.0% | 0.0% | 0.0% |
| runtime-command-matching | `BenchmarkForbiddenCommandPrepared` | 0.0% | 0.0% | 0.0% |
| runtime-command-evidence | `BenchmarkCommandEvidencePrepared` | -1.1% | 0.0% | 0.0% |
| runtime-evaluation-memos | `BenchmarkMatchContextMemoHit` | 0.0% | 0.0% | 0.0% |
| runtime-execution-input | `BenchmarkLoadExecutionInputsEvents8192` | +0.7% | 0.0% | 0.0% |
| runtime-lockfile-decode | `BenchmarkDecodeCurrentLockfileMaximumRules` | +0.9% | +0.8% | 0.0% |
| runtime-source-freshness | `BenchmarkRuntimePlanFreshnessLargeSourceSet` | -6.3% | -6.5% | -2.4% |
| runtime-source-freshness | `BenchmarkRuntimePlanConcurrentRoots` | -2.1% | -0.6% | +3.5% |
| runtime-write-epochs | `BenchmarkNormalizeWriteEpochsBatch` | -0.3% | 0.0% | 0.0% |
| session-evidence-workloads | `BenchmarkWorkerPreHookVerifiedEvidencePrefix/warm-prefix` | -0.1% | +0.1% | +1.2% |
| session-evidence-workloads | `BenchmarkWorkerPreHookVerifiedEvidencePrefix/cold-prefix` | 0.0% | 0.0% | +1.1% |

Pareto stop: the selected change removes one redundant filesystem stat without weakening any snapshot barrier and yields a repeatable 6.3% median latency and 6.5% B/op improvement in the large-source hit. The warm pre-hook remains effectively flat and the other two bounded variants did not produce a material improvement; further filesystem-call removal would require a larger identity redesign with higher correctness risk. The existing checked v4 baseline still needs a separate, deliberate v6 migration before ordinary `make benchmark-compare` can use it; this task has not refreshed it.

Final local verification: `go test -race ./internal/boundedio ./internal/runtime ./internal/runtime/agentsession -count=1`, `make test` (uncached race suites for both modules, publication audit, reference docs, harness pack, and release trust), `make vet`, `make lint`, `make self-host`, and `git diff --check` all passed. `make test` intentionally cleans ignored `.build` measurement artifacts during its isolated release-trust fixture; the source-bound measurements and selected raw distributions above remain in this task record. Hosted CI is checked separately after the pushed commit, not inferred from these local gates.

## Deviations

None.
