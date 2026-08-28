# TASK 300: Apply Windows private ACLs through opened handles

## Why

Windows private-file hardening opens a no-follow handle but applies owner and DACL changes through `SetNamedSecurityInfo(file.Name())`. A path replacement can redirect the security mutation to a different object before post-validation rejects the operation.

## Acceptance

- File and directory owner/DACL changes target the already opened handle with the exact required access rights.
- No by-path security mutation remains in privatefs or action-state durable paths.
- Reparse-point, replacement, ownership, protected-DACL, and access-denied tests run natively on Windows.
- Unix behavior remains unchanged.

## Sub-Tasks

- [x] Inventory every Windows by-path security mutation
- [x] Implement one handle-bound security descriptor writer
- [x] Add replacement and reparse-point fault injection
- [~] Run Windows-native private-state and release gates

## Notes

- Evidence: `internal/privatefs/privatefs_windows.go:75-78` and the corresponding action-state ACL path.
- Production inventory found two path-based writers: `privatefs.secureWindowsHandle`
  and the legacy Action State mode helper. PrivateFS now reopens the supplied
  object handle with exactly `WRITE_DAC|WRITE_OWNER` and applies owner plus the
  protected DACL with `SetSecurityInfo`; Action State delegates to that one
  writer. No production `SetNamedSecurityInfo` call remains.
- Windows-only regressions inject access denial while asserting exact requested
  rights, replace the path after the security handle is bound, and prove that a
  rejected reparse point leaves its target security descriptor unchanged.
- Local verification passes: `make test`, `make vet`, `make self-host`, pinned
  Staticcheck `v0.8.1` for the root and portable harness, Windows amd64/arm64
  test compilation, Windows amd64 vet, and Windows amd64 Staticcheck. The exact
  `make lint` wrapper could not query already cached module metadata because
  local DNS to `proxy.golang.org` failed; the same cached `v0.8.1` source was
  built and run directly. The mandatory native regressions are included in the
  always-on `windows-2025` preflight; native CI remains the only acceptance gate.

## Deviations

None.
