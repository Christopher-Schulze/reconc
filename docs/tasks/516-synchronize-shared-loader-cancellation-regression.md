# TASK 516: Synchronize the shared-loader cancellation regression

## Why

Native macOS run 34593702243 reports `active 0 cached 1` in the last-caller
cancellation test. The fixture releases the shared worker immediately after
canceling the caller, racing the waiter that transfers cancellation to the
separately owned worker. A fully validated plan can finish before that transfer;
this does not establish publication of a partial or canceled-worker plan.

## Acceptance

- Establish the actual cancellation ordering before releasing the paused worker.
- Preserve cancellation-error, zero active loads, zero cached plans, and valid
  subsequent retry assertions without changing production code or deadlines.
- A missing cancellation transfer must fail the regression; repeated race and
  normal tests and native macOS verification must pass.

## Sub-Tasks

- [x] Trace the failed fixture and reference-counted shared-worker ownership.
- [x] Synchronize the existing regression with the real worker cancellation.
- [~] Run focused and required gates, verify native CI, archive, commit, and push.

## Notes

- Original fixture reproduces 98 failures in 100 runs with `GOMAXPROCS=1`.
  The synchronized fixture passes all 100 runs and 25 race-enabled runs of
  canceled-caller and same-root concurrency tests. All original assertions
  remain; failure to transfer cancellation now triggers its own bounded error.
- Complete `go test -p=2 ./...`, `make vet`, and `make lint` pass. Native run
  34593995920 independently reproduces the same old fixture ordering failure;
  this candidate will verify both TASK 515 and TASK 516 corrections together.
- Source: https://github.com/Christopher-Schulze/reconc/actions/runs/34593702243.
- TASK 515 candidate 7f6069622526dcd73716df62ba565216fe7f307a is committed
  and pushed. Its native run remains useful; TASK 514 benchmark run continues.
- Read the complete regression file and the load ownership/publication path.
  The worker owns a separate context so one canceled caller cannot abort work
  still needed by another caller. The test must observe that context's Done
  signal before resuming the source-snapshot hook.

## Deviations

- The test ordering was incorrect: caller cancellation is not synchronous
  worker cancellation after TASK 505 introduced shared ownership. Correct the
  fixture ordering while retaining every behavioral assertion.
- Native proof requires a locally verified candidate commit on main under the
  standing push instruction. Keep the TASK active until that proof completes.
