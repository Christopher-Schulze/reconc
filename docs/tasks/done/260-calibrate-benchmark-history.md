# TASK 260: Calibrate benchmark history

## Why

Current benchmark documentation records useful one-machine observations, while
`make bench` produces raw absolute results with no durable comparison contract.
Absolute CI time limits are noisy across hardware. A calibrated history should
compare each hot path with a stable same-run reference and retain enough
environment metadata to explain change without pretending machines are equal.

## Acceptance

- A bounded benchmark runner records Go version, OS, architecture, CPU identity,
  repository commit, benchmark parameters, raw metrics, and same-run calibration
  metrics in a deterministic machine-readable result.
- Historical comparisons use normalized ratios within compatible benchmark
  groups and report absolute metrics separately; they refuse incompatible or
  incomplete inputs instead of fabricating a regression percentage.
- A checked baseline can be intentionally refreshed, while normal check mode is
  read-only and uses documented tolerances for meaningful sustained regressions.
- Fast pull-request verification does not run long benchmarks. Manual and
  release-oriented workflows can produce and compare history without fragile
  wall-clock gates.
- Unit tests cover parsing, normalization, compatibility, missing benchmarks,
  tolerance boundaries, deterministic output, and hostile or oversized input.
- Makefile and benchmark documentation provide concise record, compare, and
  baseline-refresh workflows.

## Sub-Tasks

- [x] Inventory benchmark families and choose stable calibration references
- [x] Define the bounded benchmark result and baseline contracts
- [x] Implement record, normalize, compare, and refresh tooling
- [x] Add Makefile and non-blocking workflow integration
- [x] Add parser, calibration, compatibility, and regression tests
- [x] Update benchmark documentation with measured-truth limits
- [x] Run focused and repository-wide verification
- [x] Archive the completed TASK and commit the verified change

## Notes

- Existing Go benchmarks are package-local and `make bench` currently uses
  `-benchtime=1000x`. The new tooling must consume standard `go test -json`
  benchmark output rather than inventing a parallel benchmark framework.
- Hardware normalization reduces noise; it does not make results from different
  operating systems, architectures, Go versions, or benchmark sets identical.
- The checked suite pairs six target paths with same-package legacy or
  independent references and records five independent 100-iteration samples.
- Real record, explicit refresh, and comparison completed on Go 1.27.0,
  Darwin/arm64, Apple M1. The checked comparison reported compatible and passed.
- Verification completed with package race tests, package vet, workflow YAML
  parsing, `make test-fast`, `make publication-audit`, and `git diff --check`.

## Deviations

None.
