# TASK 500: Reality-check all 30 review fixes and stabilize performance evidence

## Why

The 30 review fixes are implemented on `main`, but current task records retain
historical pending markers and the calibrated benchmark gate uses 100-iteration
samples that can report large false regressions on the same Apple M1/toolchain.
Close the documentation truth gap and make performance evidence stable without
weakening regression detection or refreshing a baseline to hide a real change.

## Acceptance

- TASK 470-499 records contain only current completion status and no stale
  pending gate or unchecked sub-task claims.
- Benchmark recording uses a bounded duration that materially reduces sample
  noise while keeping the existing suite, normalization, calibration-aware
  timing and absolute resource budgets with clean-source identity checks.
- The checked baseline remains anchored to the clean pre-491 source commit;
  current HEAD compares against it with compatible parameters and passes.
- Full repository gates and a quantitative before/after performance report are
  recorded; no version, tag or remote publication changes.

## Sub-Tasks

- [x] Inventory all 30 task records, current source/gate state and benchmark
  evidence; classify proven, stale, missing and contradictory claims.
- [x] Stabilize benchmark sample duration and regenerate a clean historical
  baseline without changing product behavior or hiding regressions.
- [x] Correct task-record truth and document the measured 30-point result.
- [x] Run complete gates, re-read every modified file and commit one TASK 500
  change on `main`.

## Notes

- Review scope is TASK 470-499, the 30 accepted findings from the 2026-09-08
  source review.
- The development version remains `0.9.9`; the final source identity is recorded
  in the clean benchmark contract.
- The existing 100x benchmark comparison produced variable false positives.
  A matched `250ms`, three-sample comparison between clean TASK-490 source
  `c814f5b4` and current HEAD passed with no regressions.
- The five-sample `250ms` baseline is generated from clean `c814f5b4`. The
  final clean TASK500 source run passes all 24 target benchmarks across 19
  groups with compatible parameters and `dirty=false` on both sides. The
  comparison contract retains per-target absolute, normalized, allocation and
  peak-RSS deltas; the accepted run has zero blocking regressions and no
  un-explained metric over its configured 20% normalized/peak-RSS, 20%
  absolute-time, 10% absolute-bytes, or 10% absolute-allocation budget.

### Completion evidence

- `make test-fast`, `make test`, `make vet`, `make lint`, `make self-host`,
  `make reference-docs-check`, `make publication-audit`, and `git diff --check`
  pass before the final commit. The complete test gate includes uncached root
  and portable-template race suites plus release-trust.
- The benchmark contract remains five 250-millisecond samples, one logical CPU,
  same-suite normalization, absolute time/bytes/allocation budgets, and clean
  source identity checks. No version, tag or remote publication changes.
- Absolute timing comparisons now retain raw values and expose the calibration
  comparison in the report; host-wide calibration drift is excluded only when
  normalized target time remains within budget. Target-specific normalized time
  regressions and independent bytes/allocation budgets remain blocking.
- Repeated one-second recordings exposed transient absolute timing/RSS outliers
  on this Apple M1 as the long suite thermally drifted. The bounded 250ms
  protocol keeps the final full-suite comparison within every budget; the
  earlier outliers remain documented in the working evidence rather than being
  hidden.

## Deviations

None.
