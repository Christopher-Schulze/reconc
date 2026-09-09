# TASK 505: Make shared runtime-plan loading cancellation-safe

## Why

Runtime-plan loads are coalesced, but the first caller's context currently owns the shared compilation. If that caller cancels while another caller still needs the plan, valid work is aborted. Discovery also routes through a background-context glob wrapper, so cancellation does not bound policy-fragment enumeration.

## Acceptance

- A canceled owner returns promptly while surviving waiters can receive the completed shared plan.
- A load with no surviving callers is canceled, publishes no partial cache entry, and leaves no active-load state.
- Discovery policy-fragment enumeration honors the caller context between bounded filesystem operations.
- Non-context APIs retain their behavior, nil evaluators do not panic, and deterministic cancellation/concurrency tests cover the lifecycle.

## Sub-Tasks

- [x] Add reference-counted shared-load ownership and cancellation-safe worker publication.
- [x] Thread context through discovery policy-fragment listing and add regressions.
- [x] Run runtime/ingest focused and race gates, archive this task, commit and push.

## Notes

- Discovered during the read-only reality audit of TASK 482.
- Shared loads now use an evaluator-owned context and caller reference count; the final canceled caller stops the worker, while surviving waiters receive the result.
- Discovery policy-fragment globs poll the caller context between bounded filesystem operations; the background compatibility entry point remains intact.
- Verification: focused runtime/ingest tests, targeted race tests, `make test-fast`, `make test`, `make vet`, and `make lint` passed.

## Deviations

The cancellation regression waits until the surviving waiter is registered before canceling the owner, making the ownership invariant deterministic under scheduler variation.
