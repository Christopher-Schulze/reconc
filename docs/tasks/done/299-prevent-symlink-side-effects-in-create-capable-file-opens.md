# TASK 299: Prevent symlink side effects in create-capable file opens

## Why

Private lock and JSONL live-file creation use `O_CREATE` without exclusive creation or no-follow semantics. A symlink inserted after `Lstat` can make the open create an out-of-tree target before the post-open identity check rejects it.

## Acceptance

- Absent-path creation uses a no-follow create-only operation and handles an existing-path race by reopening through the strict existing-file path.
- No rejected symlink, dangling symlink, hardlink swap, or replacement creates or modifies an unintended target.
- Existing private mode, ownership, link-count, layout security, and locking guarantees remain intact.
- Unix adversarial and Windows-equivalent tests pass.

## Sub-Tasks

- [x] Define separate absent and existing open state machines
- [x] Implement descriptor-safe no-follow creation
- [x] Add dangling-symlink and replacement side-effect tests
- [x] Run privatefs, JSONL, action-state, ledger, and race gates

## Notes

- Evidence: `internal/privatefs/privatefs.go:337-361`, `privatefs_unix.go:53-58`, and `internal/jsonl/append.go:270-293`.
- Go 1.27 `os.Root.OpenFile` already rejects symlinks on Unix and Windows, but
  `O_CREATE` without `O_EXCL` can still open a regular file that wins the
  absent-path race. Private lock creation also used an unrooted full-path open,
  so replacing an already validated parent could redirect creation.
- Both state machines now root operations at a validated parent, create with
  `O_CREATE|O_EXCL`, and reopen an `ErrExist` race only after strict entry,
  identity, security, and single-link checks. A rejected parent replacement
  removes its newly created identity through the still-open root.
- Verified with adversarial symlink, dangling-symlink, hard-link, leaf-
  replacement, and parent-replacement regressions; complete privatefs, JSONL,
  audit, action-state, and action-ledger tests under race; Windows amd64 test
  compilation and vet; `make test`, `make vet`, `make lint`, and
  `make self-host`.

## Deviations

None.
