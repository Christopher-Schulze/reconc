# TASK 410: Bound action glob matching work

## Why

Directory-star backtracking can rescan the same agent-controlled value once per backtrack level and brace alternative. Compile-time pattern bytes are bounded, but evaluation work is not directly bounded by input length times matcher states.

## Acceptance

- Glob evaluation has a documented deterministic work ceiling derived from compiled pattern and input bounds.
- Hitting the ceiling returns a fail-closed, correctly classified result rather than hanging or silently mismatching.
- Ordinary glob semantics, captures, brace alternatives, and path separators remain unchanged below the ceiling.
- Adversarial benchmarks demonstrate bounded scaling for repeated `**/` patterns and maximum-size values.

## Sub-Tasks

- [ ] Add worst-case matcher benchmarks and confirm the current scaling curve.
- [ ] Normalize redundant directory-star states and/or count bounded match work.
- [ ] Add limit classification and semantic equivalence tests.
- [ ] Run focused action glob tests and benchmarks.

## Notes

- Verified from finding 86 by inspecting `directoryPatternBacktrack` rescans.
- This is a performance/resource-bound task, not a regex ReDoS task; the separate regex operator uses bounded RE2 semantics.

## Deviations
