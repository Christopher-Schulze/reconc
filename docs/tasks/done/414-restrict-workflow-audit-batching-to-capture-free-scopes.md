# TASK 414: Restrict workflow-audit batching to capture-free scopes

## Why

Workflow-audit batching sends one input with empty captures and the complete write-path set, then applies the result to every eligible rule. Rules with capture-producing `when_paths` therefore receive different script evidence than ordinary per-context execution.

## Acceptance

- Batch eligibility requires a scope whose normal script evaluation cannot observe per-context captures.
- Capture-producing rules always use the existing per-context execution path.
- Batched and unbatched decisions, violations, triggered paths, and failure details are equivalent for eligible rules.
- Tests use scripts that inspect captures and write paths to prove unsafe batching cannot occur.

## Sub-Tasks

- [x] Define the batch protocol's capture and path-observation contract.
- [x] Gate candidates using the canonical template-variable detector.
- [x] Add capture-aware adversarial and parity tests.
- [x] Run focused runtime script tests and batching benchmarks.

## Notes

- Verified from finding 96.
- Finding 95 was rejected: an incomplete mode set is explicitly treated as an invalid batch response and safely rerun per rule; partial acceptance is an optional optimization, not a correctness defect.
- Confirmed on current source: batching collects each rule's template match contexts but discards their capture maps, then sends one synthetic input with empty captures and the complete write-path set.
- The normal per-context contract always includes the complete normalized write-path set; only template variables in `when_paths` make its script input context-dependent. `PatternHasAnyTemplateVar` is the canonical detector for that boundary.
- Capture-free per-context execution now serializes the same explicit empty capture object as the batch protocol, eliminating the prior observable `null` versus `{}` shape drift for eligible rules.
- Verification passed: focused workflow-audit batching tests, the complete `internal/runtime` package, `make test-fast`, and the singleton batching benchmark at 100 iterations (`1688 ns/op`, `226 B/op`, `4 allocs/op`).

## Deviations
