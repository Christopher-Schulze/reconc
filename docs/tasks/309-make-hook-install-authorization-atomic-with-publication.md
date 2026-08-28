# TASK 309: Make hook install authorization atomic with publication

## Why

Several hook installers decide that existing content is Reconc-managed, then publish later through a separate atomic writer. A user edit between the managed check or snapshot revalidation and replacement can still be overwritten.

## Acceptance

- Every replacement is conditional on the exact bytes and identity authorized during preflight.
- A concurrent edit before publication fails closed and remains untouched.
- Create-only, managed update, forced foreign replacement, merge, mode, and backup semantics remain explicit and distinct.
- Adversarial replacement tests cover all platform installers and merged configs.

## Sub-Tasks

- [ ] Inventory all check-then-publish hook paths
- [ ] Add an exact compare-and-publish primitive
- [ ] Migrate managed, forced, and merged installers
- [ ] Run hook race, ownership, and platform gates

## Notes

- Evidence: `internal/hooks/platform_installers.go:141-179`, `internal/hooks/hooks.go:425-448,674-705`, and activation apply paths.

## Deviations

None.
