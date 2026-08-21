# TASK 220: Resolve v6 schema publication drift

## Why

TASK 204 intentionally added the rule-kind field matrix to
`schemas/v6/policy-lock.schema.json` and updated the local registry digest, but
the current registry still publishes the immutable `reconc-v0.9.6` URL. That
tag contains the previous v6 bytes. The publication contract therefore fails
closed: the local schema is semantically newer than the bytes named by its
public URL. This cannot be repaired safely by disabling the exact-byte test,
rewriting an immutable tag, or silently reverting the schema without deciding
whether TASK 204's schema alignment or the release identity is authoritative.

## Acceptance

- An explicit release decision selects either a new immutable schema/publication
  identity for the rule-kind matrix or a reviewed rollback to the exact
  `reconc-v0.9.6` bytes; no mutable `main` or rewritten tag URL is emitted.
- `internal/schema/registry.go`, schema `$id`/references, format/migration
  declarations, release assets, checksums, RFC/docs, and generated copies are
  updated consistently with that decision.
- `TestSchemaRegistryPublicationTagsOwnExactBytesOrAuthorizedPlan` and the
  complete publication/schema/release gates pass against the selected identity.
- Existing v6 consumers and every supported legacy lock migration retain their
  declared compatibility behavior; parser/runtime rule-kind enforcement stays
  aligned with the published schema.

## Sub-Tasks

- [ ] Choose and record the immutable publication/version strategy
- [ ] Reconcile schema bytes, registry digest, IDs, references, and release assets
- [ ] Verify migration, compatibility, publication, and complete gates

## Notes

- Current failure: `scripts/audits/publication/schema_contract_test.go:TestSchemaRegistryPublicationTagsOwnExactBytesOrAuthorizedPlan` reports that `schemas/v6/policy-lock.schema.json` differs from `reconc-v0.9.6:schemas/v6/policy-lock.schema.json`.
- This TASK is queued only; no schema, tag, release, or public URL was changed while the decision is missing.

## Deviations

None.
