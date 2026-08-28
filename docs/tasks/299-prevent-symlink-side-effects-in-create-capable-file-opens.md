# TASK 299: Prevent symlink side effects in create-capable file opens

## Why

Private lock and JSONL live-file creation use `O_CREATE` without exclusive creation or no-follow semantics. A symlink inserted after `Lstat` can make the open create an out-of-tree target before the post-open identity check rejects it.

## Acceptance

- Absent-path creation uses a no-follow create-only operation and handles an existing-path race by reopening through the strict existing-file path.
- No rejected symlink, dangling symlink, hardlink swap, or replacement creates or modifies an unintended target.
- Existing private mode, ownership, link-count, layout security, and locking guarantees remain intact.
- Unix adversarial and Windows-equivalent tests pass.

## Sub-Tasks

- [ ] Define separate absent and existing open state machines
- [ ] Implement descriptor-safe no-follow creation
- [ ] Add dangling-symlink and replacement side-effect tests
- [ ] Run privatefs, JSONL, action-state, ledger, and race gates

## Notes

- Evidence: `internal/privatefs/privatefs.go:337-361`, `privatefs_unix.go:53-58`, and `internal/jsonl/append.go:270-293`.

## Deviations

None.
