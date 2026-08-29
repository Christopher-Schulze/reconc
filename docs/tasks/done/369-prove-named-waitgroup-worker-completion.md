# TASK 369: Prove named WaitGroup worker completion

## Why

Go concurrency assurance accepts `go worker(&wg)` from the argument shape plus caller-side `Add` and `Wait`, without inspecting the named worker for a matching deferred `Done`. A worker that never decrements the group can be incorrectly classified as owned.

## Acceptance

- Named-worker acceptance proves the callee invokes the matching WaitGroup `Done` on every required path, including the documented deferred form.
- Unresolved, external, ambiguous, or non-completing callees fail closed.
- Inline function-literal handling retains existing behavior.
- Tests cover missing `Done`, wrong parameter, non-deferred `Done`, aliases, methods, and valid named workers.

## Sub-Tasks

- [x] Resolve eligible named workers within the parsed package or file boundary.
- [x] Verify parameter binding and completion ownership in the callee.
- [x] Add adversarial named-worker regressions.
- [x] Run focused Go concurrency assurance tests.

## Notes

- Verified from OMP session `01a04cc5-6a01-7526-95bb-b2715553daf3`, finding #115.
- Current evidence: `internal/assurance/go_concurrency.go` accepts an address-of local WaitGroup argument without inspecting the callee body.
- Named workers are resolved only to unique same-file functions or methods, with direct function aliases limited to statements before the launch; imported, unresolved, and ambiguous targets fail closed.
- Completion requires the selected `*sync.WaitGroup` parameter (including same-file type aliases) to reach a top-level deferred matching `Done`; receiver aliases are tracked and control-flow-only or non-deferred workers fail closed.
- Focused concurrency tests, the complete `internal/assurance` package, package vet, formatting, reference-doc checks, and `git diff --check` passed.

## Deviations

- Per explicit execution instruction, the full `make test`/race/release-trust gates and local Windows test execution were not run; the retained CI matrix and platform-specific tests were not removed or disabled.
