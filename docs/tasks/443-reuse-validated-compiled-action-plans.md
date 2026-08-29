# TASK 443: Reuse validated compiled action plans

## Why

`NewEvaluator` receives a `CompiledPlan` whose fields are package-private and immutable to callers, then clones, normalizes, recompiles every glob/regex, and marshals the full plan again.

## Acceptance

- Evaluator construction reuses immutable compiled matchers and canonical plan identity without trusting mutable caller state.
- Returned plan/rule accessors remain defensive and cannot mutate evaluator-owned data.
- Validation occurs exactly once per compiled plan unless an explicitly named revalidation API is used.
- Benchmarks prove lower construction allocations/latency for representative and maximum plans with identical decisions and traces.

## Sub-Tasks

- [ ] Document `CompiledPlan` immutability and ownership invariants.
- [ ] Carry canonical identity and compiled indexes into evaluators safely.
- [ ] Add mutation-resistance, parity, and malformed-construction tests.
- [ ] Run focused action tests and benchmarks.

## Notes

- Verified from finding 176 in `internal/action/evaluator.go`.

## Deviations
