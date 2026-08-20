# TASK 175: Distinguish warned and blocked action deltas

## Why

`actionDeltas` in `internal/impactlab/action_compare.go` emits
`newly_blocked` when an outcome changes from a permitting phase outcome to a
non-permitting one, even when the candidate decision is `warn`. That same
function already emits `newly_warned`. The delta gate counts
`newly_blocked`, so a warning can be classified as a block and consume the
wrong review contract.

## Acceptance

- `newly_blocked` is emitted only for a candidate block decision under a
  documented transition table.
- Warn, approval, allow, and block transitions each produce deterministic,
  non-contradictory delta sets and review requirements.
- Tests cover every decision-pair and phase-outcome combination, including
  warn-to-block and allow-to-warn, and fail on the current conflation.
- Summary counters, manifests, CI reports, and delta-gate review accounting use
  the corrected classification without compatibility ambiguity.

## Sub-Tasks

- [x] Define the complete action-delta transition matrix
- [x] Correct delta classification and review-gate accounting
- [x] Propagate semantics to reports and reviewed-delta manifests
- [x] Add exhaustive table-driven regression tests
- [x] Run impact, report, and complete Go gates

## Notes

- Evidence: `actionDeltas`, `actionOutcomePermits`, and
  `initializeDeltaGate` in `internal/impactlab/action_compare.go`.
- Canonical matrix: every changed decision emits `decision`; any lower-strength
  candidate also emits `newly_allowed`; candidate `warn`, `require_approval`,
  and `block` decisions exclusively emit `newly_warned`,
  `newly_approval_required`, and `newly_blocked`, respectively. Phase-outcome
  changes emit only the independent `phase_outcome` delta.
- Summary counters and text/JSON/CI projections already consume the distinct
  delta kinds. Review initialization, manifest validation, manifest matching,
  and partial-review accounting intentionally gate only `newly_allowed` and
  `newly_blocked`; removing the false block classification propagates without
  a compatibility alias or schema change.
- The exhaustive regression matrix covers all 16 decision pairs across all
  four eligible/blocked phase-outcome pairs. Dedicated accounting assertions
  prove warning and approval phase changes increment their own counters and do
  not create block-review requirements.
- Verification: focused `go test ./internal/impactlab` and the complete
  repository `make test` gate pass, including root and harness race suites,
  report packages, publication audit, harness-pack verification, and
  release-trust failure paths.

## Deviations

None.
