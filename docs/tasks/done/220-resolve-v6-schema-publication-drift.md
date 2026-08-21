# TASK 220: Resolve v6 schema publication drift

## Why

TASK 204 intentionally added the rule-kind field matrix to
`schemas/v6/policy-lock.schema.json` and updated the local registry digest, but
the current registry still publishes the immutable `reconc-v0.9.6` URL. That
tag contains the previous v6 bytes. The publication contract therefore fails
closed: the local schema is semantically newer than the bytes named by its
public URL. This cannot be repaired safely by disabling the exact-byte test,
rewriting an immutable tag, or silently reverting the schema. The selected
strategy is a new unreleased source/release identity `0.9.7`: only format 6
moves to `reconc-v0.9.7`; unchanged contracts remain pinned to the already
published `reconc-v0.9.6` bytes and identities.

## Acceptance

- The selected immutable publication identity is `reconc-v0.9.7` for format 6;
  no mutable `main` or rewritten tag URL is emitted. The tag is intentionally
  an authorized unreleased identity until the explicit release workflow
  publishes it.
- `internal/schema/registry.go`, the v6 schema `$id`/references, release
  version metadata, release assets, RFC/docs, and generated copies are
  consistent; unchanged contracts retain their exact `reconc-v0.9.6`
  identities.
- `TestSchemaRegistryPublicationTagsOwnExactBytesOrAuthorizedPlan`, schema
  validation, local release-artifact verification, and the complete local
  publication gates pass against the selected identity. Online publication
  verification remains a release-workflow responsibility and is not claimed
  before the tag exists remotely.
- Existing v6 consumers and every supported legacy lock migration retain their
  declared compatibility behavior; parser/runtime rule-kind enforcement stays
  aligned with the published schema.

## Sub-Tasks

- [x] Choose and record the immutable publication/version strategy
- [x] Reconcile schema bytes, registry digest, IDs, references, and release assets
- [x] Verify migration, compatibility, publication, and complete gates

## Notes

- The original failure was `schemas/v6/policy-lock.schema.json` differing from
  `reconc-v0.9.6:schemas/v6/policy-lock.schema.json` after TASK 204.
- Format 6 now emits `reconc-v0.9.7`, retains the old v6 URL as an input-only
  compatibility alias, and leaves all unchanged schema files byte-identical.
- `reconc-v0.9.7` is not present on `origin` yet. The local publication test
  treats the current source tag as the authorized publication plan; the online
  HTTP verifier must be run only after the exact protected tag is published.
- A full race-enabled run exposed a same-process audit append lock-polling
  storm under host load. `internal/audit` now serializes append bursts per
  audit directory before the existing bounded cross-process lock; the
  concurrency stress test and complete root/template race suites pass without
  weakening durability or changing the public audit format.
- Verified locally with `go run ./scripts/audits/publication --root .`,
  `go run ./scripts/build/harness-pack --check`, `go test -race -count=1
  -timeout 20m ./...`, `(cd harness/template && go test -race -count=1 ./...)`,
  `make test-release-trust`, and `make release VERSION=0.9.7` including
  manifest/checksum verification. `git diff --check` is clean.

## Deviations

The remote tag/release publication is intentionally not performed in this
local task because the repository release workflow requires an explicit
protected-tag dispatch. This does not weaken any local exact-byte or release
inventory gate.
