# TASK 370: Preserve prerelease precedence in harness compatibility

## Why

Harness compatibility parsing strips prerelease and build suffixes before range comparison. A prerelease such as `0.9.0-rc.1` can therefore satisfy a stable `0.9.0` minimum, and `1.0.0-rc.1` can be treated as equal to an exclusive stable maximum.

## Acceptance

- Compatibility checks implement SemVer precedence for prerelease versions.
- Build metadata does not affect precedence.
- Inclusive and exclusive minimum and maximum bounds handle stable and prerelease endpoints correctly.
- Table-driven tests cover SemVer precedence edge cases and malformed versions.

## Sub-Tasks

- [x] Reuse or centralize the repository's existing full SemVer implementation.
- [x] Replace core-only harness range comparisons.
- [x] Add stable, prerelease, and build-metadata regressions.
- [x] Run focused harness compatibility tests.

## Notes

- Verified from OMP session `01a04cc5-6a01-7526-95bb-b2715553daf3`, finding #116.
- Current evidence: `internal/harnesspack/pack.go` removes `-` and `+` suffixes before comparing numeric components.
- Shared strict parsing and precedence now live in `internal/semver`; harness compatibility accepts prerelease and build metadata, while the release lifecycle keeps its existing release-tag build-metadata restriction through a thin wrapper.
- Compatibility tests cover stable/prerelease inclusive and exclusive endpoints, arbitrary-length numeric prerelease identifiers, build metadata equality, and malformed inputs.
- Focused semver, harness-pack, usercli semantic, and harness compile tests, package vet, formatting, reference-doc checks, and `git diff --check` passed.

## Deviations

- Per explicit execution instruction, the full `make test`/race/release-trust gates and local Windows test execution were not run; the retained CI matrix and platform-specific tests were not removed or disabled.
