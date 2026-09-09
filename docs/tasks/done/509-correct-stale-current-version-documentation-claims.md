# TASK 509: Correct stale current-version documentation claims

## Why

The canonical release-state section records source version v0.9.9, but four
current-source/component statements in `docs/documentation.md` still say
v0.9.8. Agents and operators can therefore select the wrong release identity
when reading the otherwise current guide.

## Acceptance

- Every current-source/component statement identified by the audit says v0.9.9.
- Historical release, migration, installer, and command-example references to v0.9.8 remain unchanged.
- A repository-wide version search distinguishes the remaining historical references from the corrected current state.

## Sub-Tasks

- [x] Verify the exact stale statements against the canonical release-state source and classify historical references.
- [x] Update only the stale current-source/component statements.
- [x] Re-read, search, run documentation checks, archive this detail, and push one TASK commit.

## Notes

### Review provenance

- Follow-up to the TASK 495 audit: stale current-version claims remained in `docs/documentation.md` after the compact skill and guide were corrected.

### Verification

- Updated exactly four current-source/component claims to v0.9.9; historical release, migration, installer, and command-example references remain intact.
- `make reference-docs-check` and `git diff --check` passed.

## Deviations

None planned.
