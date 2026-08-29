# TASK 348: Bind private parent security to opened descriptors

## Why

Private-file opening validates parent security by path and later binds a separately opened parent descriptor. A path replacement between those operations can bind an owner, mode, or ACL state that was never security-validated.

## Acceptance

- Parent ownership, permissions, and platform ACL checks apply to the descriptor used for the private-file operation.
- Parent replacement between validation and binding fails closed.
- Unix and Windows implementations preserve their platform-specific security guarantees.
- Adversarial tests cover replacement before and after parent descriptor acquisition.

## Sub-Tasks

- [x] Extend private parent binding to validate security through the opened descriptor.
- [x] Remove reliance on a disconnected path-only security decision.
- [x] Add cross-platform replacement-race regressions.
- [x] Run private filesystem tests on supported platforms.

## Notes

- Verified from OMP session `01a04cc5-6a01-7526-95bb-b2715553daf3`, finding #93.
- Reverified the gap in the current code: parent security was checked through a path-opened descriptor before a separate parent root was bound.
- `openPrivateFileParent` now validates the root-bound parent descriptor; all private opens, including read-only existing locks, use that validated binding.
- Replacement hooks prove rejection before and after parent descriptor acquisition; rejected paths never create a lock in either directory.
- `go test ./internal/privatefs` passed on macOS. Windows code paths were preserved and not executed locally.
- TASK 300 hardened Windows descriptor mutation, not this parent-binding gap.

## Deviations

None.
