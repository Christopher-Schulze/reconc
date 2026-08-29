# TASK 414: Restrict workflow-audit batching to capture-free scopes

## Why

Workflow-audit batching sends one input with empty captures and the complete write-path set, then applies the result to every eligible rule. Rules with capture-producing `when_paths` therefore receive different script evidence than ordinary per-context execution.

## Acceptance

- Batch eligibility requires a scope whose normal script evaluation cannot observe per-context captures.
- Capture-producing rules always use the existing per-context execution path.
- Batched and unbatched decisions, violations, triggered paths, and failure details are equivalent for eligible rules.
- Tests use scripts that inspect captures and write paths to prove unsafe batching cannot occur.

## Sub-Tasks

- [ ] Define the batch protocol's capture and path-observation contract.
- [ ] Gate candidates using the canonical template-variable detector.
- [ ] Add capture-aware adversarial and parity tests.
- [ ] Run focused runtime script tests and batching benchmarks.

## Notes

- Verified from finding 96.
- Finding 95 was rejected: an incomplete mode set is explicitly treated as an invalid batch response and safely rerun per rule; partial acceptance is an optional optimization, not a correctness defect.

## Deviations
