# TASK 305: Reject extension-only policy targets

## Why

Policy-author target validation accepts `policies/.yml` and `policies/.yaml` because it checks only the extension and non-empty path component, not a non-empty filename stem.

## Acceptance

- Policy targets require a non-empty valid stem before `.yml` or `.yaml`.
- Hidden filenames are either explicitly supported by a precise rule or rejected consistently; extension-only names are always rejected.
- CLI diagnostics identify the invalid target without changing valid direct-child behavior.
- Policy-author unit and integration tests pass.

## Sub-Tasks

- [x] Define the target basename contract
- [x] Tighten validation without altering safe existing names
- [x] Add extension-only and boundary tests
- [x] Run policy-author and CLI gates

## Notes

- Evidence: `internal/policyauthor/types.go:186-199`.
- Contract: after removing the case-insensitive `.yml` or `.yaml` suffix, the direct-child filename stem must contain at least one non-dot character. Named hidden targets such as `policies/.private.yml` remain valid; extension-only and dot-only stems do not.
- Verification: Policy Author and CLI regressions, `make test`, `make vet`, pinned Staticcheck v0.8.1, and `make self-host` pass.

## Deviations

None.
