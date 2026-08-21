# TASK 181: Store audit evidence with a private JSONL layout

## Why

Audit entries can contain repository paths, claims, rule identities, and
normalized command evidence. `audit.Append` uses the generic JSONL layout,
whose live file, archives, lock, and parent defaults are `0644`/`0755`, while
the detached audit head is `0600`. That split makes retained evidence more
widely readable than its integrity metadata.

## Acceptance

- Audit live, archive, lock, journal, backup, head, and parent modes are defined
  by one stable private layout and used consistently by append, recovery,
  verification, tail, and retention.
- Existing audit rings are migrated or rejected through an explicit,
  non-destructive compatibility path; no evidence is silently discarded.
- Layout security validates identities and modes before reading or mutating any
  ring member.
- Tests cover first creation, legacy-mode migration, rotation, crash recovery,
  hostile symlinks, and concurrent readers/writers.
- Audit schema, hash-chain semantics, size bounds, and CLI output do not drift.

## Sub-Tasks

- [x] Define the canonical audit JSONL layout
- [x] Thread it through every audit and retention entry point
- [x] Implement safe legacy-mode handling
- [x] Add permission, rotation, recovery, and concurrency tests
- [x] Run audit, JSONL, race, and complete gates

## Notes

- Evidence: `internal/audit/audit.go:122-194` and
  `internal/jsonl/jsonl.go:65-70`.
- `internal/audit/layout.go` defines the stable private JSONL layout: `.reconc`
  mode `0700`; live/archive/head/lock/journal/backup members mode `0600`;
  private identity/owner/security validation; and a two-minute bounded lock.
  Audit append, recovery, verification, tail, export, and retention all use
  the same layout identity.
- Legacy directory/file modes are migrated in place only after non-symlink
  regular-file checks. JSONL rotation's intentional live/backup hard-link
  window is allowed for content members, while the lock remains single-link;
  hostile links/special files fail before mutation. A pre-private journal is
  recovered explicitly through the legacy layout fallback, then the private
  detached head is published.
- The prepared-layout cache rechecks the parent and all retained members on
  every use, so externally reintroduced legacy modes are migrated before the
  next read or mutation rather than hidden by a stale cache entry.
- Retention inspection now validates and reads the chained ring through the
  same private audit lock and snapshot path before reporting cleanup.
- Added permission/migration, hostile lock-symlink, rotation, recovery, and
  concurrent-writer coverage. `make publication-audit` passed; Windows audit,
  JSONL, and privatefs test binaries cross-compiled; and the complete `make
  test` gate passed, including race-enabled packages, harness tests, and
  release trust.

## Deviations

None.
