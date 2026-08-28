# TASK 293: Make Windows atomic replacement genuinely write-through

## Why

The Windows durability contract says replacements use write-through publication, but `replaceFile` only calls `os.Root.Rename` while parent sync is a no-op. The implementation does not perform the claimed write-through operation.

## Acceptance

- Windows replacement uses a native write-through primitive without escaping the validated parent identity.
- Existing create-only, replacement, symlink, reparse-point, and rollback semantics remain fail closed.
- Comments, architecture, and RFC text describe the actual supported durability boundary.
- Native Windows fault-injection and complete CI gates pass.

## Sub-Tasks

- [ ] Select and verify the exact rooted Win32 replacement primitive
- [ ] Implement write-through replacement with precise error mapping
- [ ] Add native durability and identity regressions
- [ ] Reconcile documentation and run platform gates

## Notes

- Evidence: `internal/atomicfile/replace_windows.go` and `internal/atomicfile/sync_dir_windows.go`; TASK 239 claims a property the current code does not implement.

## Deviations

None.
