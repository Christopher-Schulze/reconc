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
  `https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.4/schemas/v4/policy-lock.schema.json`.
- `format_version`: minor format marker inside the same schema URL.

Additive fields with clear defaults may keep the schema URL and bump
`format_version` when needed. Removing, repurposing, or changing the
type of an existing field requires a new schema URL and a superseding
RFC.
