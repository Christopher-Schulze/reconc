# TASK 239: Restore descriptor-safe Windows filesystem compatibility

## Why

The first GitHub Actions run after the Go 1.27 and filesystem-hardening work
exposed three Windows-specific contract mismatches. Atomic publication tries
to flush a read-only directory handle, private-state hardening tries to update
a DACL through handles that lack `WRITE_DAC`, and release-notice discovery
rejects the trusted setup-go GOROOT junction before resolving its filesystem
identity. These failures block the Windows build and smoke gate even though
the same candidate passes locally, on macOS, and on Ubuntu.

## Acceptance

- Atomic publication preserves the strongest supported Windows durability
  boundary without calling `FlushFileBuffers` through an incompatible
  read-only directory handle.
- Private directory and file DACLs are applied through identity-checked handles
  opened with the exact Windows access rights required to update and validate
  their security descriptors.
- Go toolchain notice discovery resolves the trusted GOROOT identity before
  enforcing strict non-symlink directory and regular-file reads.
- Existing adversarial DACL, reparse-point, atomic-publication, release-notice,
  and cross-platform behavior remains fail-closed.
- Formatting, module tidiness, tests, race tests, Vet, Staticcheck, release
  trust, publication, harness-pack, and Windows CI checks pass.
- The fix is committed and pushed, with local `main`, `origin/main`, and the
  clean worktree agreeing afterward. No tag or release is created.

## Sub-Tasks

- [x] Bind each Windows CI failure to its exact platform contract
- [x] Correct descriptor access and supported durability behavior
- [x] Resolve trusted Go toolchain directory identity before notice reads
- [x] Add or retain regression coverage and run the full local gate set
- [x] Re-read the diff, archive the TASK, commit, push, and verify remote CI

## Notes

- GitHub Actions run `32519379380` passed macOS, Ubuntu, LangChain MCP
  interoperability, and release-trust jobs. Only Windows failed.
- Microsoft documents that `FlushFileBuffers` requires a handle with
  `GENERIC_WRITE`, while `DACL_SECURITY_INFORMATION` and
  `PROTECTED_DACL_SECURITY_INFORMATION` require `WRITE_DAC`.
- The failures repeat across consumers because the affected operations are
  centralized in `internal/atomicfile` and `internal/privatefs`; they are not
  independent package regressions.
- Windows private-state directories and files now open through no-follow
  descriptors carrying `WRITE_DAC`; the existing identity and protected-DACL
  validation remains unchanged and fail-closed.
- Windows parent-directory sync now reflects the actual Win32 boundary:
  payload files are synced, replacements retain write-through publication,
  and an incompatible read-only directory `FlushFileBuffers` call is not made.
- Go toolchain notice collection resolves the GOROOT filesystem identity before
  strict directory and license-file reads. A regression test covers a trusted
  linked root while the existing symlinked-license rejection remains intact.
- Verification passed locally: focused affected-package tests, Windows/amd64
  test-binary cross-compilation, module-tidy diffs, `make test` with root and
  harness race suites plus release trust, Vet, Staticcheck v0.8.0, all 58 root
  fuzz targets, and the isolated self-hosting proof.

## Deviations

None.
