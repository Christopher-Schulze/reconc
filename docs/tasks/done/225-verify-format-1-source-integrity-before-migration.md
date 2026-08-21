# TASK 225: Verify format-1 source integrity before migration

## Why

Format-1 policy locks predate `lock_digest`, but they already carry a
`source_digest` over the format-1 `source_precedence` and raw `sources`
representation. `migrateLockfileV1ToV2` currently validates the legacy schema
and physical repository fields, then creates a format-2 whole-lock digest
without first proving that the stored format-1 source digest matches the
embedded source records. Runtime loading later rebinds migrated locks to live
sources, but `MigrateLockfile` also serves read-only inspection, lock-diff, and
repository-sync code. Migration must not promote unverified legacy source
content into a newly self-digested payload, and the documented claim that
legacy digests pass before migration must be true at the migration boundary.

## Acceptance

- The 1-to-2 migration recomputes the exact historical format-1 source digest
  from `source_precedence` and `sources` before changing any field.
- The verifier implements the format-1 canonical representation, not the
  current format-6 provenance representation; raw legacy source bodies,
  optional block metadata, ordering, and JSON number behavior remain exact.
- Missing, malformed, non-lowercase, or mismatched `source_digest` values fail
  the migration before a format-2 `lock_digest` is generated.
- Source-body, source-path, source-kind, block metadata, source-order, and
  precedence tampering are covered with independently failing tests.
- A genuine format-1 fixture produced by the historical compiler migrates
  deterministically through format 6 and retains current runtime source,
  embedded-rule, default-mode, and canonical-action parity checks.
- Format-2-through-format-6 migration behavior, current-version no-op behavior,
  schema acceptance, caller immutability, and error typing remain unchanged.
- Doctor/status summaries, lock diffing, schema validation, and repository-sync
  planning reject a tampered format-1 payload consistently.
- Repository policy documentation states the exact format-1 limitation: source
  integrity is verified, while full-payload integrity was unavailable before
  `lock_digest` was introduced in format 2.

## Sub-Tasks

- [x] Recover and pin the historical format-1 source-digest algorithm
- [x] Add a bounded pure verifier to the 1-to-2 migration boundary
- [x] Add genuine-fixture, tamper-matrix, determinism, and caller tests
- [x] Verify every production `MigrateLockfile` consumer fails consistently
- [x] Correct legacy-migration documentation and run compiler/runtime gates

## Notes

- Session finding: `#5`.
- Primary code: `internal/compiler/migrations.go`,
  `internal/runtime/lockfile.go`, `internal/cli/inspect_cmd.go`,
  `internal/lockdiff/diff.go`, and
  `internal/bootstrap/repository_sync_plan.go`.
- Historical format-1 source-digest behavior is available from the immutable
  `reconc-v0.4.0` compiler and must be converted into a maintained golden
  fixture or test oracle rather than approximated from current structures.
- This TASK does not invent authenticity for format 1. It proves consistency
  between the stored digest and embedded source records; migrated runtime use
  must still bind the payload to current repository sources.
- Verification: affected package tests and race tests passed for compiler,
  runtime, CLI, lockdiff, bootstrap, and schema; affected-package vet passed.

## Deviations

None.
