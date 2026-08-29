# TASK 329: Linearize session-state normalization

## Why

Session normalization sorts and deduplicates collections, clears them, then rebuilds each through append helpers that repeatedly rescan retained bytes and existing values. Maximum path and command collections therefore normalize in quadratic time.

## Acceptance

- Each normalized collection computes membership and retained bytes once.
- Item order, trimming, exact versus trimmed fields, byte and count limits, overflow reason, write epochs, and pending-call order remain unchanged.
- Maximum-state normalization scales linearly after sorting and has allocation regression coverage.
- Session-state, evidence-segment, Stop, race, and benchmark gates pass.

## Sub-Tasks

- [x] Capture normalized-state golden and scaling baselines
- [x] Rebuild collections with prepared seen and byte state
- [x] Preserve overflow and epoch semantics
- [x] Add maximum-state allocation and complexity tests

## Notes

- Evidence: `internal/runtime/agentsession/state_limits.go:36-105`.
- String collections now sort and deduplicate once, then rebuild with one retained-byte pass; command results use one normalized identity map and one encoded-byte accumulator.
- Write-epoch entries retain the pre-existing normalization behavior, including entries whose over-limit write path is rejected while its epoch remains recorded.
- Golden, overflow/epoch, concurrent, and maximum-state benchmark coverage passed. `go test ./internal/runtime/agentsession -count=1`, the package race suite, `make test-fast`, `make vet`, `make lint`, and `make self-host` are green.
- Documentation now states that mutations normalize and compare once before deciding whether to publish.

## Deviations

None.
