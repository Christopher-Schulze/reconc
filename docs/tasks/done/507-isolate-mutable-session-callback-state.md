# TASK 507: Isolate mutable session callback state

## Why

`MutateSessionState` passes callbacks a shallow `SessionState` copy. In-place edits to nested maps, slices, or pointer fields can therefore mutate the loaded baseline before the equality check, causing a real callback change to be discarded as a no-op.

## Acceptance

- Every mutable session-state field is isolated before each callback invocation, including the evidence-rotation retry.
- Nested JSON-compatible `PendingToolCall.ToolInput` values and pointer fields cannot mutate the loaded baseline through aliasing.
- Nil versus empty collection semantics remain stable and existing normalized publication behavior is unchanged.
- Regression tests prove nested callback mutations persist and all required repository gates pass.

## Sub-Tasks

- [x] Deep-clone callback input state without changing adapter helper clone contracts.
- [x] Add nested map/slice/pointer regression coverage, including rotation retry.
- [x] Run focused and complete gates, flush docs, archive this task, commit and push.

## Notes

- Discovered during the read-only reality audit of TASK 487.
- `MutateSessionState` now deep-clones every mutable session field before the initial callback and evidence-rotation retry. JSON-compatible tool-input maps, slices, arrays, pointers, command-result pointers, write epochs, tombstones, and approval collections receive independent storage while nil collections remain nil.
- Regression coverage proves nested tool-input, command-result pointer, and Antigravity high-water mutations persist without changing the loaded baseline. Focused package and race tests pass.
- Verification: `make test-fast` and complete `make test` passed with isolated temporary Go caches; `make vet`, `make lint`, `make self-host`, and `git diff --check` passed.

## Deviations

None planned.
