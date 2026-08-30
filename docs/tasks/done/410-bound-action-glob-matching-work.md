# TASK 410: Bound action glob matching work

## Why

Directory-star backtracking can rescan the same agent-controlled value once per backtrack level and brace alternative. Compile-time pattern bytes are bounded, but evaluation work is not directly bounded by input length times matcher states.

## Acceptance

- Glob evaluation has a documented deterministic work ceiling derived from compiled pattern and input bounds.
- Hitting the ceiling returns a fail-closed, correctly classified result rather than hanging or silently mismatching.
- Ordinary glob semantics, captures, brace alternatives, and path separators remain unchanged below the ceiling.
- Adversarial benchmarks demonstrate bounded scaling for repeated `**/` patterns and maximum-size values.

## Sub-Tasks

- [x] Add worst-case matcher benchmarks and confirm the current scaling curve.
- [x] Normalize redundant directory-star states and/or count bounded match work.
- [x] Add limit classification and semantic equivalence tests.
- [x] Run focused action glob tests and benchmarks.

## Notes

- Verified from finding 86 by inspecting `directoryPatternBacktrack` rescans.
- This is a performance/resource-bound task, not a regex ReDoS task; the separate regex operator uses bounded RE2 semantics.
- Before the bound, 64 KiB non-matches across 16, 64, 256, and 1,024 brace programs took 1.81 ms, 7.25 ms, 29.32 ms, and 116.17 ms in one-iteration Apple M1 benchmarks. The shared 16,777,216-unit ceiling reduced the 256- and 1,024-program cases to about 21 ms while maximum-size repeated-directory-star matching remained linear and allocation-free.
- The runtime path-matcher caller preserves its legacy bool semantics through its existing validated doublestar fallback if the action-payload ceiling is exhausted. Focused action and runtime tests, the adversarial benchmarks, `make test-fast`, and `git diff --check` passed.

## Deviations
