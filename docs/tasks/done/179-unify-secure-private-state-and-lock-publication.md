# TASK 179: Unify secure private state and lock publication

## Why

Private state packages independently create directories and lock files with
`MkdirAll`, path-based chmod, and `OpenFile(O_CREATE)`. Receipt, retention,
command-proof, and some action-state paths therefore have inconsistent
symlink, hard-link, ownership, mode, and identity-swap guarantees. Reconc
already has stronger patterns in secured JSONL and action-state code, but they
are not a shared boundary.

## Acceptance

- One reusable internal primitive defines private directory creation, existing
  directory validation, lock publication, open-descriptor validation, owner and
  mode checks, and cleanup on every supported platform.
- Receipt, retention, command-proof, action-state, and other private-state
  callsites use that primitive where their threat model matches.
- No path-based chmod occurs before the opened inode and current directory entry
  are proven identical; unexpected hard-link aliases are rejected where private
  single-link ownership is required.
- Tests cover existing and raced symlinks, hard links, irregular files, unsafe
  modes, wrong owners, concurrent first creation, and Windows behavior.
- No state location, filename, retention behavior, or public JSONL contract
  changes implicitly.

## Sub-Tasks

- [x] Inventory private directory and lock creation callsites
- [x] Specify one cross-platform secure filesystem contract
- [x] Implement and test the shared primitive
- [x] Migrate matching callsites without behavior drift
- [x] Run security, race, and complete Go gates

## Notes

- Verified examples: `internal/usercli/receipt.go`,
  `internal/retention/prune.go`, `internal/commandproof/proof.go`, and
  `internal/actionstate/secure_fs.go`.
- The session claim that `RECONC_HOME` uses `os.ExpandEnv` is false in the
  current code and is not part of this TASK.
- Added `internal/privatefs` as the shared descriptor-first boundary. It
  creates components without chmodding public ancestors, rejects final
  symlinks/irregular entries, validates current-user ownership and Unix
  single-link state, applies private mode/security through opened descriptors,
  and revalidates the exact directory entry before returning a lock.
- Migrated action-state lock/directory helpers, installation receipts,
  retention locks/markers, command proofs, and unresolved policy proofs to the
  shared boundary. `RepairDirectory` is used only for legacy retention roots;
  state locations, filenames, retention rules, and JSON contracts are unchanged.
- Tests cover symlink, hard-link, irregular target, unsafe-mode repair,
  missing-target, and concurrent first-lock creation. Windows cross-compilation
  and race-enabled focused package tests passed.
- The first complete gate exposed that JSONL rotation intentionally retains
  hard-linked archive/live files. The single-link check remains limited to
  private lock publication; JSONL content validation preserves its existing
  archive semantics. The corrected `make test` passed publication audit,
  harness-pack checks, the complete race-enabled Go suite, harness template
  race tests, and release trust.

## Deviations

None.
