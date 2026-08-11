# reconc RFCs

This directory contains frozen implementation contracts for `reconc`.
They describe JSON artifacts, rule semantics, packaged policy packs,
and hook behavior that downstream agents and automation may depend on.

RFCs are not roadmap notes. A frozen RFC describes behavior enforced by
the current implementation. If code and RFC disagree, treat that as a
bug in whichever side is stale and fix them together.

## Status

| Status | Meaning |
|---|---|
| Draft | Proposed but not yet enforced. Do not build integrations against it. |
| Frozen | Current enforced contract. Consumers may depend on it. |
| Superseded | Replaced by a newer RFC. Kept for history only. |

## Index

| RFC | Status | Contract |
|---|---|---|
| RECONC-0001 | Frozen | Policy lockfile |
| RECONC-0002 | Frozen | Check report |
| RECONC-0003 | Frozen | Fix plan |
| RECONC-0004 | Frozen | Rule kinds |
| RECONC-0005 | Frozen | Presets and templates |
| RECONC-0006 | Frozen | Hooks and agent sessions |
| RECONC-0007 | Frozen | v0.9 CLI product, ownership, update, and repository synchronization |
| RECONC-0008 | Draft | Go-only Action Plane |

## Versioning

Every JSON contract has:

- `$schema`: hard compatibility boundary, for example
  `https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/schemas/v6/policy-lock.schema.json`.
- `format_version`: exact representation marker owned by that schema version.

Every published schema URL is immutable. Any schema-byte or represented-shape
change, including an additive field or a new format marker, requires a newly
versioned schema URL and registry entry. Removing, repurposing, changing a
type, or changing semantics additionally requires a superseding RFC.

`internal/schema` is the single registry for public JSON Schema contracts.
Each artifact has exactly one current schema version; every retained legacy
version remains independently registered with its local path, immutable
release asset, introduction tag, canonical SHA-256, supported format versions,
and explicit compatibility aliases. A decoder accepts only a registered
schema-URL and format-version pair. Unknown future versions and crossed pairs
fail closed.

Default URLs name immutable release tags and must return bytes whose `$id` and
SHA-256 equal the registry. Compatibility aliases are input-only: Reconc never
emits an unpinned, missing-tag, or otherwise historical alias, and accepting an
alias does not claim that its old location serves the current local bytes.
`RECONC_SCHEMA_BASE_URL` resolves a configured enterprise identity at
`/schemas/<artifact>/v<schema-version>`; portable public exports keep their
registered public default identity so private infrastructure does not leak.
Runtime validation is offline and never fetches a schema. Network retrieval is
restricted to publication verification.
