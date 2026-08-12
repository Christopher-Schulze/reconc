# TASK 168: Reconcile v0.9.6 publication truth

## Why

The final release-readiness pass found three current user-facing statements
that still described `v0.9.6` as planned or called `v0.9.5` current. The final
release notes also need to state the private JSONL first-publication fix shipped
after the initial `v0.9.6` replacement attempt.

## Acceptance

- Current documentation identifies `reconc-v0.9.6` as the immutable release.
- Migration guidance distinguishes source version from exact tag identity
  without claiming that publication is pending.
- Release notes describe atomic private lock publication and bounded concurrent
  directory observation without overstating weaker existing-state repair.
- Publication and release-trust audits pass with source version exactly `0.9.6`.

## Sub-Tasks

- [x] Audit current release-state wording outside historical TASK records
- [x] Correct stale documentation and complete the v0.9.6 release notes
- [x] Run publication and release-trust audits

## Notes

Historical migration statements for older releases remain intact.

The first release-trust rerun correctly rejected measured percentages in TASK
167 as a forbidden project-text pass/fail contract. The note now records that
measurement completed without creating a numeric policy.

The publication audit and the complete real-target release-trust audit passed
after the correction.

## Deviations

None.
