# TASK 293: Make Windows atomic replacement genuinely write-through

## Why

The Windows durability contract says replacements use write-through publication, but `replaceFile` only calls `os.Root.Rename` while parent sync is a no-op. The implementation does not perform the claimed write-through operation.

## Acceptance

- Windows replacement uses a native write-through primitive without escaping the validated parent identity.
- Existing create-only, replacement, symlink, reparse-point, and rollback semantics remain fail closed.
- Comments, architecture, and RFC text describe the actual supported durability boundary.
- Native Windows fault-injection and complete CI gates pass.

## Sub-Tasks

- [x] Select and verify the exact rooted Win32 replacement primitive
- [x] Implement write-through replacement with precise error mapping
- [x] Add native durability and identity regressions
- [~] Reconcile documentation and run platform gates

## Notes

- Evidence: `internal/atomicfile/replace_windows.go` and `internal/atomicfile/sync_dir_windows.go`; TASK 239 claims a property the current code does not implement.
- Microsoft documents that `FILE_WRITE_THROUGH` flushes metadata changes,
  including rename operations, and that `FILE_RENAME_INFORMATION.RootDirectory`
  makes the destination relative to an opened directory handle. The
  implementation therefore uses `NtCreateFile` with `OBJ_DONT_REPARSE` and
  `FILE_WRITE_THROUGH`, then `NtSetInformationFile` with the validated
  `os.Root` directory handle. Path-based `MoveFileExW` was rejected because it
  would reconstruct authority from mutable path text.
- `FileRenameInformationEx` preserves replacement/POSIX behavior where the
  filesystem supports it; the legacy rooted information class is the bounded
  compatibility fallback. Native status errors are converted to Win32 errno
  values before the existing cleanup and rollback path receives them.
- Local verification passes: `make test`, `make vet`, `make lint`, Windows
  amd64/arm64 test cross-compilation, Windows amd64 `go vet`, and Windows amd64
  Staticcheck. The mandatory native fault-injection tests are included in the
  always-on `windows-2025` preflight, but no local Windows runtime exists and
  repository rules prohibit pushing the uncommitted task without explicit user
  authorization. Native CI is the only remaining acceptance gate.

## Deviations

None.
