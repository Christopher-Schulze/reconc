# TASK 307: Make malformed-config backup identities collision-resistant

## Why

Malformed hook-config backups use only the first 32 bits of SHA-256 in their filename. A different file with the same short prefix blocks a forced repair even though the implementation detects the mismatch.

## Acceptance

- New backup names use a collision-resistant full-content identity.
- Existing short-name backups are recognized only when their complete bytes match and are never overwritten.
- Concurrent identical and colliding backup attempts remain create-only and deterministic.
- Hook install, recovery, security, and platform tests pass.

## Sub-Tasks

- [ ] Define backward-compatible backup lookup and naming
- [ ] Publish full-digest create-only backups
- [ ] Add synthetic-prefix collision and concurrency tests
- [ ] Run hook install and recovery gates

## Notes

- Evidence: `internal/hooks/hooks.go:627-648`.

## Deviations

None.
