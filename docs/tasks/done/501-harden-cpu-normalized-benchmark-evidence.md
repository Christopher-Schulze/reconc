# TASK 501: Harden CPU-normalized benchmark evidence

## Why

TASK 500 reduced benchmark noise, but its same-package calibration heuristic
could hide a real equal slowdown when a target and its calibrator degraded
together. The performance gate needs an independent CPU measurement so host
drift is adjusted without treating application regressions as calibration noise.

## Acceptance

- Every recorded package sample includes an independent CPU sentinel measurement
  bracketed around the product benchmarks.
- Comparison reports raw target timing, same-package calibration timing, and
  CPU-sentinel timing separately; only CPU-adjusted absolute timing is gated.
- Equal target/calibrator slowdown with an unchanged CPU sentinel fails, while
  matching host-wide CPU drift is adjusted and remains attributable.
- Result, baseline, and comparison contracts reject stale schemas; the clean
  baseline remains anchored to `c814f5b40579d2feed47231ec9dc2d80afda1dea`.
- Full repository gates and a clean current comparison pass without version,
  tag, branch, or remote publication changes.

## Sub-Tasks

- [x] Add independent sentinel collection, contract fields, validation, and
  CPU-adjusted comparison semantics.
- [x] Regenerate and verify the clean v3 baseline and current comparison.
- [x] Update benchmark documentation and record final gate evidence.

## Notes

- Scope is the TASK 490 performance-evidence gap discovered during the TASK 500
  reality check.
- The sentinel is generated in a temporary module-free package and is never
  added to the product source tree.
- The v3 baseline records clean source commit
  `c814f5b40579d2feed47231ec9dc2d80afda1dea`; the matching clean current run
  covers 19 groups and 24 targets with `passed=true` and no blocking
  regressions. Raw absolute timing, CPU-adjusted timing, normalized timing,
  bytes, allocations, and peak RSS remain separately reported.
- `make test-fast`, `make test`, `make vet`, `make lint`, `make self-host`,
  `make reference-docs-check`, `make publication-audit`, and `git diff --check`
  pass. No version, tag, branch, push, or publication changed.

## Deviations

None.
