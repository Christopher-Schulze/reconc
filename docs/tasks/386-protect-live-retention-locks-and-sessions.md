# TASK 386: Protect live retention locks and sessions

## Why

Retention can unlink a lock file held by another process, split future lock acquisition onto a new inode, discard an explicitly supplied live session because it is old or has not written state yet, and remove an aged project root while another process still owns live state below it.

## Acceptance

- A lock candidate is deleted only after a non-blocking exclusive lock proves it is unheld on the same validated identity.
- An explicit `ActiveSession` remains protected regardless of age; age applies only to passive pointer discovery.
- Project-root deletion requires a validated liveness or lease decision and never relies on age alone.
- Count and byte-pressure pruning cannot bypass any live-object protection.
- Deterministic multi-process tests cover held locks, release races, old and not-yet-persisted explicit sessions, active aged roots, and stale passive pointers.

## Sub-Tasks

- [ ] Separate explicit active-session authority from passive age inference.
- [ ] Probe lock ownership before deletion while preserving identity revalidation.
- [ ] Bind project-root pruning to a live lease or equivalent validated ownership signal.
- [ ] Add deterministic lock-inode, session-age, first-write, and aged-root regressions.
- [ ] Run focused retention and agent-session tests.

## Notes

- Verified from findings 26, 27, 148, and 160.
- `pruneClass("locks", ...)` currently treats ordinary lock files as removable candidates; `liveActiveSession` applies `Locks.MaxAge` to both requested and discovered identities.
- Project-root pressure uses mtime/age without proving that no process still owns the root; an explicit new session is dropped when its state file does not exist yet.

## Deviations
