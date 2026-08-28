# TASK 300: Apply Windows private ACLs through opened handles

## Why

Windows private-file hardening opens a no-follow handle but applies owner and DACL changes through `SetNamedSecurityInfo(file.Name())`. A path replacement can redirect the security mutation to a different object before post-validation rejects the operation.

## Acceptance

- File and directory owner/DACL changes target the already opened handle with the exact required access rights.
- No by-path security mutation remains in privatefs or action-state durable paths.
- Reparse-point, replacement, ownership, protected-DACL, and access-denied tests run natively on Windows.
- Unix behavior remains unchanged.

## Sub-Tasks

- [ ] Inventory every Windows by-path security mutation
- [ ] Implement one handle-bound security descriptor writer
- [ ] Add replacement and reparse-point fault injection
- [ ] Run Windows-native private-state and release gates

## Notes

- Evidence: `internal/privatefs/privatefs_windows.go:75-78` and the corresponding action-state ACL path.

## Deviations

None.
