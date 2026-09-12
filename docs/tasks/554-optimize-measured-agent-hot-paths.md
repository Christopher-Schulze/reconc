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

- [ ] Record a clean post-integration baseline and bounded CPU/allocation/IO profiles.
- [ ] Select at most three dominant cost centers and document expected gain and invariants.
- [ ] Implement and verify one measured change at a time.
- [ ] Run paired comparisons and the complete benchmark inventory; report the Pareto stopping decision.

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

Retained TASK 539 measurements include roughly 665,122 B/op and 5,800 allocations on the warm session-pre target, and 747,158 B/op and 6,367 allocations cold. Raw tail timings were not uniformly better than the old baseline despite passing calibrated budgets. These are historical comparison points only. No full fresh benchmark was run in this planning task.

## Deviations

None. If profiles establish that remaining gains are marginal, record a measured no-change conclusion and remaining headroom; do not describe an unperformed optimization as implemented.
