# TASK 330: Avoid duplicate full session-state equality passes

## Why

Every session mutation performs `reflect.DeepEqual` before normalization and, when changed, another complete `reflect.DeepEqual` afterward. Large evidence collections are traversed twice even for ordinary mutations.

## Acceptance

- Mutation publication decides no-op versus write with at most one full normalized-state comparison.
- A mutator whose apparent change normalizes away still performs no write.
- Missing files and permission repair continue to force publication when required.
- No-op, normalize-away, maximum-state, race, and allocation tests pass.

## Sub-Tasks

- [ ] Characterize every current no-op and repair branch
- [ ] Normalize once and compare once without aliasing input state
- [ ] Add write-call-count and maximum-state tests
- [ ] Run session, Stop, and race gates

## Notes

- Evidence: `internal/runtime/agentsession/state.go:544-573`.

## Deviations

None.
