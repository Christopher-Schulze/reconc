# TASK 329: Linearize session-state normalization

## Why

Session normalization sorts and deduplicates collections, clears them, then rebuilds each through append helpers that repeatedly rescan retained bytes and existing values. Maximum path and command collections therefore normalize in quadratic time.

## Acceptance

- Each normalized collection computes membership and retained bytes once.
- Item order, trimming, exact versus trimmed fields, byte and count limits, overflow reason, write epochs, and pending-call order remain unchanged.
- Maximum-state normalization scales linearly after sorting and has allocation regression coverage.
- Session-state, evidence-segment, Stop, race, and benchmark gates pass.

## Sub-Tasks

- [ ] Capture normalized-state golden and scaling baselines
- [ ] Rebuild collections with prepared seen and byte state
- [ ] Preserve overflow and epoch semantics
- [ ] Add maximum-state allocation and complexity tests

## Notes

- Evidence: `internal/runtime/agentsession/state_limits.go:36-105`.

## Deviations

None.
