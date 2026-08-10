# reconc v0.9.6

Reconc v0.9.6 restores immutable public-schema truth before introducing the
Go-only Action Plane. It preserves the current policy-lock format while making
every emitted schema identity, retained compatibility input, release asset, and
publication check derive from one exact registry.

## Added

- A typed per-artifact registry for all current and legacy JSON Schema
  contracts, including immutable default URLs, local and release paths,
  introduction tags, SHA-256 digests, enterprise mirror paths, supported
  formats, state, and input-only aliases.
- Independent offline Draft 2020-12 compilation for every registered schema,
  representative validation for every current and legacy artifact, and
  end-to-end validation of every legacy policy-lock migration.
- Release-time online verification that every published canonical schema URL
  returns HTTP 200 without redirects and is byte-identical to the registered
  local file.

## Changed

- Current policy authoring, repository-sync plan/report, and custom-runtime
  manifests use v2 schemas. Restored v1 inputs remain readable; a legacy
  custom-runtime route must still budget enough bytes for the current canonical
  response metadata, while v2 makes the safe 512-byte minimum explicit.
- Release copying, verification, checksums, manifest, SBOM, and provenance
  inventories consume the registry directly instead of maintaining a separate
  schema list.
- Custom-runtime manifest v2 requires a response budget large enough to hold
  the canonical neutral-response metadata introduced by release-pinned schema
  identities.

## Fixed

- Schema files no longer claim mutable, missing-tag, or unreachable canonical
  locations. Historical identities remain explicit compatibility aliases and
  are never emitted as verified publication URLs.
- Semantic additions are no longer retroactively attributed to v1 policy,
  repository-sync, or custom-runtime contracts.
- Policy-lock v2 and v3 use truthful immutable identities, while the current
  v4 file remains byte-identical to its v0.9.4 tag.
- RFC 0001 consistently identifies policy-lock format 4, and the RFC index now
  states the immutable schema-evolution rule enforced by the registry.

## Compatibility

- Current policy locks remain format `4` and retain the immutable v0.9.4
  `schemas/v4/policy-lock.schema.json` identity.
- Supported legacy schema aliases and policy-lock formats 1 through 3 continue
  to migrate offline. Unknown URLs, crossed URL/format pairs, and future
  versions fail closed.
- Core runtime behavior makes no schema-network request. Online retrieval is a
  release-publication gate only.

## Upgrade

After the immutable release is published:

```bash
reconc update
reconc doctor --global
```

Exact native install on macOS or Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.6/install.sh \
  | sh -s -- --version 0.9.6
export PATH="$HOME/.local/bin:$PATH"
reconc doctor --global
```

Repository policy locks do not require a migration. Re-run `reconc refresh .`
only when policy sources change or when you intentionally want newly emitted
artifact identities.
