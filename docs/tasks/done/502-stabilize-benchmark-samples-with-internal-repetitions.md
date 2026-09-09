# TASK 502: Stabilize benchmark samples with internal repetitions

## Why

TASK 501 added independent CPU-sentinel normalization, but repeated clean
comparisons still show transient process and scheduler outliers because each
outer sample contains only one measurement per benchmark command. Collapse a
small bounded set of internal Go benchmark repetitions into each sample so the
gate measures a stable median without weakening target-specific or absolute
regression detection.

## Acceptance

- Benchmark recording defaults to three internal `go test -count` repetitions
  per outer sample and records that parameter in result and profile contracts.
- Every benchmark and CPU-sentinel sample collapses exactly the configured
  repetitions deterministically; malformed or incomplete output fails closed.
- Result, baseline, and comparison contracts reject stale schemas or mismatched
  repetition parameters, while raw, normalized, CPU-adjusted, byte,
  allocation, and RSS regressions remain gated as before.
- A clean baseline remains anchored to
  `c814f5b40579d2feed47231ec9dc2d80afda1dea`; repeated current comparisons and
  the complete repository gates pass without version, tag, branch, or remote
  publication changes.

## Sub-Tasks

- [x] Add bounded repetition parameters, deterministic aggregation, validation,
  and focused tests.
- [x] Regenerate and verify the clean baseline and repeated current comparison.
- [x] Update benchmark documentation and record gate evidence.
- [x] Re-read modified files, archive this task, commit, and push every task
  commit to `origin/main`.

## Notes

- Scope is benchmark-history evidence only; product behavior is unchanged.
- The default internal repetition count is three and is bounded to 1..5 to keep
  the full suite finite and comparable across machines.
- Result, baseline, and comparison contracts are v4, v4, and v5 respectively;
  all include `repetitions=3` and reject mismatched parameters.
- The stable baseline result was recorded from clean c814f5b4 and published in
  commit `9450cbdf`; it covers 19 groups, 24 targets, and five outer samples.
- A clean current run from commit `53238607` passed against that baseline with
  zero gated regressions: normalized ns -18.60..+6.76%, CPU-sentinel
  calibration -10.22..+8.01%, CPU-adjusted absolute ns -19.02..+13.99%,
  normalized bytes -8.99..+4.98%, absolute bytes -0.00..+3.96%, allocations
  -0.08..+0.39%, and peak RSS -0.79..+4.07%. The only difference between that
  clean measured source and the final HEAD is the baseline contract itself.
- Two earlier baseline captures and three current captures with CPU-adjusted
  failures were retained as noise evidence while unrelated workspaces drove
  high host load; no tolerance or gate was weakened to make them pass.
- `make test-fast`, `make test`, `make vet`, `make lint`, `make self-host`,
  `make reference-docs-check`, `make publication-audit`, and `git diff --check`
  passed.

## Deviations

None.
