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

- [~] Add bounded repetition parameters, deterministic aggregation, validation,
  and focused tests.
- [ ] Regenerate and verify the clean baseline and repeated current comparison.
- [ ] Update benchmark documentation and record gate evidence.
- [ ] Re-read modified files, archive this task, commit, and push every task
  commit to `origin/main`.

## Notes

- Scope is benchmark-history evidence only; product behavior is unchanged.
- The default internal repetition count is three and is bounded to 1..5 to keep
  the full suite finite and comparable across machines.

## Deviations

None.
