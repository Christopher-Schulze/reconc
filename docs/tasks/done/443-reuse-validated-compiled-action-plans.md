# TASK 443: Reuse validated compiled action plans

## Why

`NewEvaluator` receives a `CompiledPlan` whose fields are package-private and immutable to callers, then clones, normalizes, recompiles every glob/regex, and marshals the full plan again.

## Acceptance

- Evaluator construction reuses immutable compiled matchers and canonical plan identity without trusting mutable caller state.
- Returned plan/rule accessors remain defensive and cannot mutate evaluator-owned data.
- Validation occurs exactly once per compiled plan unless an explicitly named revalidation API is used.
- Benchmarks prove lower construction allocations/latency for representative and maximum plans with identical decisions and traces.

## Sub-Tasks

- [x] Document `CompiledPlan` immutability and ownership invariants.
- [x] Carry canonical identity and compiled indexes into evaluators safely.
- [x] Add mutation-resistance, parity, and malformed-construction tests.
- [x] Run focused action tests and benchmarks.

## Notes

- Verified from finding 176 in `internal/action/evaluator.go`.
- `CompilePlan` is the sole constructor of validated compiled state; all fields are package-private, inputs are deeply cloned, and exported accessors return detached values. `NewEvaluator` can therefore share the immutable plan, matcher graph, and indexes without trusting caller-owned memory.
- Baseline to optimized `NewEvaluator`: representative plan about 165 us / 221 KB / 287 allocations to 1.25 us / 382 B / 1 allocation; maximum plan about 12.6 ms / 19.5 MB / 32,872 allocations to below 1 us / 383 B / 1 allocation.

## Deviations
