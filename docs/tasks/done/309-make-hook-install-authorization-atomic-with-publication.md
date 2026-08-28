# TASK 309: Make hook install authorization atomic with publication

## Why

Several hook installers decide that existing content is Reconc-managed, then publish later through a separate atomic writer. A user edit between the managed check or snapshot revalidation and replacement can still be overwritten.

## Acceptance

- Every replacement is conditional on the exact bytes and identity authorized during preflight.
- A concurrent edit before publication fails closed and remains untouched.
- Create-only, managed update, forced foreign replacement, merge, mode, and backup semantics remain explicit and distinct.
- Adversarial replacement tests cover all platform installers and merged configs.

## Sub-Tasks

- [x] Inventory all check-then-publish hook paths
- [x] Add an exact compare-and-publish primitive
- [x] Migrate managed, forced, and merged installers
- [x] Run hook race, ownership, and platform gates

## Notes

- Evidence: `internal/hooks/platform_installers.go:141-179`, `internal/hooks/hooks.go:425-448,674-705`, and activation apply paths.
- `atomicfile.WriteIfCurrent` binds existing publication to the caller's exact bytes, identity, mode, size, and modification time; its missing expectation is create-only.
- All installable kinds converge on `publishManagedArtifact`; Kimi Code install and uninstall retain the config mode while using the same conditional boundary.
- Focused `internal/atomicfile` and complete `internal/hooks` tests pass, including adversarial stale-byte, same-byte identity-replacement, concurrent-create, mode-only, and every-installable-kind cases.
- Final gates: `make test`, `make vet`, Staticcheck v0.8.1, and `make self-host` pass.

## Deviations

None.
