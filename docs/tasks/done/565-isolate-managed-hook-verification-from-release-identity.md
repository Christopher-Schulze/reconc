# TASK 565: Isolate managed hook verification from release identity

## Why

The release gate inherited RELEASE_TAG in a test that explicitly requires a
development binary. The production build correctly selected the release
version, contradicting the fixture's development-version assertion.

## Acceptance

- The managed hook fixture builds a development binary despite an ambient
  release tag, without weakening version, adapter, or binary-digest assertions.
- The targeted race test and root Go suite pass; production release identity
  resolution remains unchanged.

## Sub-Tasks

- [x] Trace the release failure through the fixture and version resolver.
- [x] Override RELEASE_TAG only on the fixture's make command and inject an
  invalid ambient tag to make regression coverage independent of CI context.
- [x] Run the targeted race test and root suite, inspect the final diff, and archive.

## Notes

Release run 34782051932 failed in
TestManagedCLIHookVerifyCompletesMatrixWithoutChangingBinary with a valid
release version where the fixture expected dev+. No release was published.

Validation: the targeted uncached race test passed; `go test -p=4 ./...`
passed across the root module. All existing fixture assertions remain intact.

## Deviations

No product behavior or user-facing command documentation changes are required.
