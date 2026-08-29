# TASK 372: Match go:embed hidden-file semantics in provenance

## Why

Build provenance excludes every hidden path element before matching `go:embed` patterns unless `all:` is used. Go includes hidden files matched by explicit patterns such as `assets/*`, so an embedded hidden asset can change the binary without changing Reconc's source digest.

## Acceptance

- Provenance file discovery matches the active Go toolchain's `go:embed` semantics for explicit patterns, directory patterns, and `all:` prefixes.
- Hidden files included by Go are included in the source digest.
- Hidden files excluded by Go remain excluded.
- Fixture tests compare Reconc discovery against real Go embedding behavior.

## Sub-Tasks

- [x] Model the Go toolchain's pattern-specific hidden-file rules.
- [x] Update provenance embed matching without broadening unrelated source discovery.
- [x] Add explicit-pattern, directory-pattern, and `all:` fixtures.
- [x] Run focused provenance tests and a real build comparison.

## Notes

- Verified from OMP session `01a04cc5-6a01-7526-95bb-b2715553daf3`, finding #118.
- Current evidence: `buildprovenance/provenance.go` filters hidden path elements before matching; the Go `embed` implementation includes hidden files for explicit wildcard matches.
- Embedded matching now distinguishes the explicitly matched directory root from descendants: wildcard/file patterns include hidden direct matches, directory recursion excludes hidden `.`/`_` names unless prefixed with `all:`.
- Fixtures compare all three pattern forms against `go list -json` from the active Go toolchain and prove an explicitly matched hidden asset changes the production source digest.
- Focused provenance tests, usercli compile, package vet, formatting, reference-doc checks, and `git diff --check` passed.

## Deviations

- Per explicit execution instruction, the full `make test`/race/release-trust gates and local Windows test execution were not run; the retained CI matrix and platform-specific tests were not removed or disabled.
