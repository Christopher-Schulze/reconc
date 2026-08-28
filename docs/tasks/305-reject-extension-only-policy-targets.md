# TASK 305: Reject extension-only policy targets

## Why

Policy-author target validation accepts `policies/.yml` and `policies/.yaml` because it checks only the extension and non-empty path component, not a non-empty filename stem.

## Acceptance

- Policy targets require a non-empty valid stem before `.yml` or `.yaml`.
- Hidden filenames are either explicitly supported by a precise rule or rejected consistently; extension-only names are always rejected.
- CLI diagnostics identify the invalid target without changing valid direct-child behavior.
- Policy-author unit and integration tests pass.

## Sub-Tasks

- [ ] Define the target basename contract
- [ ] Tighten validation without altering safe existing names
- [ ] Add extension-only and boundary tests
- [ ] Run policy-author and CLI gates

## Notes

- Evidence: `internal/policyauthor/types.go:186-199`.

## Deviations

None.
