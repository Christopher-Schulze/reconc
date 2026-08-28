# TASK 307: Make malformed-config backup identities collision-resistant

## Why

Malformed hook-config backups use only the first 32 bits of SHA-256 in their filename. A different file with the same short prefix blocks a forced repair even though the implementation detects the mismatch.

## Acceptance

- New backup names use a collision-resistant full-content identity.
- Existing short-name backups are recognized only when their complete bytes match and are never overwritten.
- Concurrent identical and colliding backup attempts remain create-only and deterministic.
- Hook install, recovery, security, and platform tests pass.

## Sub-Tasks

- [x] Define backward-compatible backup lookup and naming
- [x] Publish full-digest create-only backups
- [x] Add synthetic-prefix collision and concurrency tests
- [x] Run hook install and recovery gates

## Notes

- Evidence: `internal/hooks/hooks.go:627-648`.
- Contract: a byte-identical legacy eight-hex-digit backup remains canonical;
  an absent or colliding legacy path selects the complete 64-hex-digit SHA-256
  name. Both paths are verified without replacing existing content.
- Verification: atomicfile and hooks tests, the concurrent race suite, Windows
  amd64 test-binary compilation, the complete release-trust gate, vet,
  Staticcheck v0.8.1, and isolated self-hosting passed.

## Deviations

None.
