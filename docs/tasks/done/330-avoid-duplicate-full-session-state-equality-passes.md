# TASK 330: Avoid duplicate full session-state equality passes

## Why

Every session mutation performs `reflect.DeepEqual` before normalization and, when changed, another complete `reflect.DeepEqual` afterward. Large evidence collections are traversed twice even for ordinary mutations.

## Acceptance

- Mutation publication decides no-op versus write with at most one full normalized-state comparison.
- A mutator whose apparent change normalizes away still performs no write.
- Missing files and permission repair continue to force publication when required.
- No-op, normalize-away, maximum-state, race, and allocation tests pass.

## Sub-Tasks

- [x] Characterize every current no-op and repair branch
- [x] Normalize once and compare once without aliasing input state
- [x] Add write-call-count and maximum-state tests
- [x] Run session, Stop, and race gates

## Notes

- Evidence: `internal/runtime/agentsession/state.go:544-573`.
- `mutateSessionStateResolved` now normalizes the candidate unconditionally and performs one `reflect.DeepEqual` against the already normalized loaded state. No-op repair checks remain after that comparison, so missing files and non-private modes still publish.
- Tests cover byte/modtime-preserving normalize-away mutations, missing-state publication, maximum-state equality, concurrent mutation, and a maximum-state allocation benchmark. `make test-fast`, `make vet`, `make lint`, `make self-host`, and the package race suite are green.

## Deviations

None.
