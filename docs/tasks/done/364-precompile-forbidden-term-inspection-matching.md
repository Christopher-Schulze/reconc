# TASK 364: Precompile forbidden-term inspection matching

## Why

Forbidden-data inspection normalizes each configured term inside each sliding-window callback and rescans the full window set once per term. At policy limits this repeats large amounts of deterministic string work under a tight deadline.

## Acceptance

- Forbidden terms are normalized once per detector pack or scan, not once per window.
- Window traversal does not restart independently for every term when an equivalent bounded algorithm is available.
- Matching semantics, confusable handling, limits, and cancellation remain unchanged.
- Benchmarks cover maximum legal text and term counts.

## Sub-Tasks

- [x] Measure current worst-case term and window scanning costs.
- [x] Precompute normalized forbidden-term matching state.
- [x] Implement a bounded single-pass or equivalent low-repetition matcher.
- [x] Add equivalence tests and run focused benchmarks.

## Notes

- Verified from OMP session `01a04cc5-6a01-7526-95bb-b2715553daf3`, finding #110.
- Current evidence: `scanTerms` now normalizes each forbidden term once per scan and passes the resulting immutable slice through one bounded `matchWindows` traversal; each window checks the prepared terms without restarting traversal or repeating normalization.
- The boundary-preserving equivalence test covers a normalized full-width term crossing the 64 KiB scan chunk boundary. The maximum legal benchmark covers 8 MiB text and all 256 legal terms: 144.1 ms/op and 4,864 B/op with one allocation on the current Apple M1 run.

## Deviations

- The matcher keeps the existing bounded window/overlap algorithm and cancellation checkpoints; no automaton or new dependency was introduced because the prepared single traversal removes the repeated normalization and window setup without changing matching semantics.
- The repository-wide race, release-trust, and other heavy suites were not run, per the explicit execution constraint; Windows-specific tests were not run locally.
