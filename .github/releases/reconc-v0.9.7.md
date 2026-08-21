# reconc v0.9.7

Reconc v0.9.7 is the source candidate for the next immutable release. It
keeps the format-6 policy-lock representation and migration chain stable while
publishing the rule-kind field matrix under a new schema identity.

## Schema and compatibility

- Format 6 is published as
  `https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.7/schemas/v6/policy-lock.schema.json`.
- The schema rejects rule fields that are unsupported by the declared rule kind
  before runtime evaluation.
- Existing format-6 locks naming the v0.9.6 schema remain accepted through an
  input-only compatibility alias; refreshing a lock emits the v0.9.7 identity.
- Formats 1 through 5 retain their exact previously published schema URLs and
  migration behavior. No format-version migration is required.
- Unchanged public schema contracts remain byte-identical at their existing
  immutable v0.9.6 identities.

## Runtime and evidence hardening

- Parser cardinality, text, and template-variable limits are enforced at the
  parser boundary.
- Immutable action plans and action context roots are reused safely within an
  evaluation, while argument sizing and evidence matching avoid duplicate
  work.
- Runtime evidence deduplication and extracted rule identifiers are bounded and
  collision-resistant.
- Harness-pack payload limits are checked before retention, and generated
  publication artifacts are refreshed from the canonical pack source.
- Proof bundles expose only sanitized executable identities; command hashes do
  not encode raw arguments or full command lines.
- Lockfile diffs report semantic, provenance, generated, unsupported, envelope,
  source-inventory, and rule-provenance changes deterministically.
- Review-relevant lockfile changes are reported with complete provenance,
  including generated-only changes that do not alter semantic policy.

## Verification

The candidate is validated by the repository's format, schema, race, release
inventory, publication-audit, harness-pack, and interoperability gates. The
remote schema and release-asset HTTP checks must be run by the protected,
tag-bound release workflow after `reconc-v0.9.7` is published.

## Upgrade

After the immutable release is published, use the existing installation owner:

```bash
reconc update
reconc doctor --global
```

Repositories with an existing format-6 lock do not need a migration. Run
`reconc refresh .` when intentionally rewriting the lock with the v0.9.7
schema identity and review the policy source and lockfile together.
