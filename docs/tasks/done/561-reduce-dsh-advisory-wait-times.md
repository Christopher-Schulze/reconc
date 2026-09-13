# TASK 561: Reduce DSH advisory wait times

## Why

Advisory DSH callbacks currently await session setup and policy work with separate multi-second budgets. Feedback must not introduce lengthy dispatch delays.

## Acceptance

- Agent step, pre-tool, and Stop callbacks share one 500 ms advisory deadline per invocation, including session setup and queue waiting; normal successful feedback remains available.
- Timeout or host cancellation cancels only the caller's pending evaluation and always preserves host continuation. Shared session setup is bounded independently and cannot accumulate unbounded waiters.
- Callback timers/listeners are released on settlement; worker generation, byte limits, passive evidence, and bounded disposal remain intact.
- Offline generated-extension regressions cover slow setup, slow evaluation, queue congestion, cancellation, normal feedback, and disposal. No DSH installation or host run.
- Docs, scaffold, archive, relevant tests/build and quality gates match the new behavior; archive, commit and push this task separately.

## Sub-Tasks

- [x] Add a shared callback deadline and bounded session setup without changing other hosts.
- [x] Add timing and cancellation regressions and propagate generated assets and documentation.
- [x] Verify implementation and prepare archived completion.

## Notes

Use a scoped AbortController and one deadline covering waitForSession plus transport.run. Session setup is shared, so a single caller's cancellation must not cancel other callers. Bound its own worker deadline to 500 ms. Preserve worker transport's absolute admission deadline and never replay ambiguous requests. The time budget bounds asynchronous waiting, not host event-loop scheduling or synchronous input serialization. Existing real Go-worker adapter tests must still receive actual policy feedback.

## Deviations

Verification: generated-extension setup/evaluation/Stop stalls and combined 450 ms setup plus 450 ms evaluation complete through the shared 500 ms deadline (asserted 400-800 ms including scheduler allowance). Real Go-worker feedback and cancellation/resource contracts pass. `make test-fast build vet lint TEST_PARALLELISM=4` passed both complete modules, and the targeted DSH race suite passed. Scaffold and deterministic pack match the reviewed source. Bun 1.3.14 matches CI; no DSH host was installed or run.

None.
