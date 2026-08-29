# TASK 326: Surface repeated-stop state mutation failures

## Why

`recordStopBlockAndRepeated` suppresses session-state mutation errors. It still returns a feedback identifier, but repeated-block detection silently degrades and the caller cannot distinguish a first block from failed durable repeat tracking.

## Acceptance

- Repeat-state mutation failure is returned or emitted through the bounded hook diagnostic contract.
- A failed write never claims that a block was durably recorded or repeated.
- The control response remains fail closed and valid for every supported host.
- State corruption, permission, lock-timeout, repeated-block, and adapter tests pass.

## Sub-Tasks

- [x] Extend the repeat-tracking result with explicit failure
- [x] Propagate failure without breaking host JSON contracts
- [x] Add durable and failed-repeat regressions
- [x] Run Stop, adapter, session-state, and race gates

## Notes

- Evidence: `internal/runtime/agentsession/stop_git.go:515-526` and caller `handlers.go:914`.
- `stopBlockRecord` now keeps the mutation error separate from repeated state and
  feedback; failed or unconfirmed persistence emits a bounded warning while the
  host response remains `decision: "block"`.
- Regression coverage verifies durable first/repeated feedback, corrupt-state
  failure handling, unchanged corrupt bytes, bounded diagnostics, and Cursor
  block-response validity.

## Deviations

None.
