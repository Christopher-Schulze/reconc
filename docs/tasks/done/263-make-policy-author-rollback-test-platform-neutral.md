# TASK 263: Make policy-author rollback test platform-neutral

## Why

The first complete Windows CI run for the consolidated main line proved the
policy-author rollback restored target and lock bytes, but its test compared
the resulting mode to the Unix literal `0640`. Windows exposes the same
writable file through Go as `0666`, so the assertion encoded an unsupported
platform representation rather than the rollback contract.

## Acceptance

- The rollback regression compares the post-rollback observable mode with the
  same file's observable pre-transaction mode on each platform.
- The test still fails when rollback changes target bytes, lock bytes, or a
  platform-representable mode bit.
- Policy-author rollback runs in the focused Windows runtime preflight so this
  contract fails within minutes rather than near the end of the full suite.
- Focused local tests, Windows compilation, publication/release trust, and the
  final GitHub CI and CodeQL checks pass directly on `main`.

## Sub-Tasks

- [x] Correct the rollback mode assertion without weakening byte checks
- [x] Add policy-author rollback to the Windows preflight
- [x] Run focused local and Windows compile verification
- [x] Commit and push directly to main
- [x] Verify final GitHub CI and CodeQL
- [x] Archive the completed TASK

## Notes

- GitHub CI run `32581976772`, job `97052610190`, failed only
  `TestApplyRollsBackTargetAndLockAfterVerificationFailure`: target and lock
  restoration passed, while Windows reported `-rw-rw-rw-` for a file created
  with Go mode `0640`.
- Focused race testing, a real `windows/amd64` test-binary compile, the expanded
  Windows preflight, `make test-fast`, publication audit, generated references,
  harness-pack verification, and complete release trust pass locally. Final
  completion is reported only after GitHub observes the committed main source.

## Deviations

None.
