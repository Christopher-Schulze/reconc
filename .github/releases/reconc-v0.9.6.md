# reconc v0.9.6

Reconc v0.9.6 restores immutable public-schema truth and introduces the first
Go-only Action Plane layer. It compiles strict action authoring into one
canonical format-6 policy lock while making every emitted schema identity,
retained compatibility input, release asset, and publication check derive from
one exact registry.

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
- Strict `actions.tools`, `actions.rules`, and `actions.defaults` authoring with
  typed selectors, effects, phases, conditions, decisions, failure/cache policy,
  provenance, deterministic normalization, and frozen resource bounds.
- `reconc why action` for redacted explanation of canonical action policy,
  defaults, provenance, selectors, and legacy lowering.
- Strict `actions.ledger` authoring with required, best-effort, or disabled
  recording, bounded selected fields, and declaration, exact, or keyed tool
  identity.
- A separate private format-1 Action Ledger with nine payload-free lifecycle
  events, domain-separated selected-field identities, atomic multi-process
  append, bounded rotation, crash recovery, archive continuity, and detached
  chain-head verification.
- `reconc action log tail|stats|verify|export` for deterministic verified
  lifecycle inspection and privacy-bounded minimized Impact Lab export. Missing
  state is read-only empty; corruption fails; export omissions and replay gaps
  remain explicit; output is private and create-only.
- `reconc mcp gateway` as a Go-only, tools-only stdio enforcement boundary
  around one operator-selected downstream MCP server. Routed calls receive
  strict tool-contract validation, policy and executable identity resampling,
  cumulative budgets, signed one-time approvals, required lifecycle recording,
  inspected progress and results, bounded stderr diagnostics, and owned child
  process-tree shutdown.
- Current MCP `2026-07-28` and legacy `2025-11-25` tool-call interoperability
  through the pinned official Go MCP SDK `v1.7.0`, including signed current
  input-required and legacy form-elicitation approvals, with direct/native tool
  bypasses and unsupported capabilities kept explicit.

## Changed

- Current policy authoring uses v4, repository-sync plan/report and
  custom-runtime manifests use v2. Restored legacy inputs remain readable; a legacy
  custom-runtime route must still budget enough bytes for the current canonical
  response metadata, while v2 makes the safe 512-byte minimum explicit.
- Current Action Ledger events use schema v2 for gateway approval and delivery
  semantics; the published v1 schema remains byte-identical and registered as
  a legacy contract.
- Release copying, verification, checksums, manifest, SBOM, provenance, project
  license, and exact third-party notices consume their canonical inventories
  instead of maintaining parallel release lists.
- Custom-runtime manifest v2 requires a response budget large enough to hold
  the canonical neutral-response metadata introduced by release-pinned schema
  identities.
- Policy-lock format 6 stores one canonical `actions` plan. Legacy `mcp`
  authoring lowers into it, existing host MCP consumers derive their
  compatibility view from it, and no parallel runtime `mcp` plan remains.

## Fixed

- Native Windows state validation accepts Windows-normalized multi-ACE DACLs
  only when their complete effective and inherited access remains owner-only
  and full. Action-ledger live files, archives, locks, journals, and recovery
  backups now enforce that same private DACL contract instead of relying on
  POSIX mode checks. Concurrent active-session publication keeps every pointer read
  and replacement under the same lock, and release publication waits for the
  exact tag to pass native Windows tests, binary smoke, and installer gates.
- Private Action Ledger lock creation now secures and verifies a same-directory
  candidate before atomically publishing the final lock path. Concurrent
  creators converge on that one protected file, transient directory-snapshot
  changes are retried within a strict bound, and existing permission, ACL,
  symlink, special-file, or identity drift still fails without repair.
- Private project-directory initialization now serializes creation and Windows
  DACL publication under the retention lock, so concurrent first-use processes
  cannot observe a partially secured directory while existing unsafe state
  still fails without repair.
- Schema files no longer claim mutable, missing-tag, or unreachable canonical
  locations. Historical identities remain explicit compatibility aliases and
  are never emitted as verified publication URLs.
- Semantic additions are no longer retroactively attributed to v1 policy,
  repository-sync, or custom-runtime contracts.
- Policy-lock v2 and v3 use truthful immutable identities, while the legacy v4
  file remains byte-identical to its v0.9.4 tag.
- RFC 0001 consistently identifies policy-lock format 6, and the RFC index now
  states the immutable schema-evolution rule enforced by the registry.
- Action globs and regexes are precompiled, strict URL/path/CIDR operands are
  canonicalized once, source precedence matches its declared contract, and all
  action-plan views are defensive copies.
- Action-ledger rotation now journals backup preparation before any primary
  mutation, rejects permission drift without repair, preserves existing generic
  JSONL modes, and enforces exact approval, budget, and terminal ordering.
- Action-ledger denial evidence now matches persisted budget state: it binds the
  live reservation, released capacity, and denied-count-only consumption.
- Action-ledger selected-field identities now bind repository and declaration
  identity, strict phase/source ownership, and explicit unavailable-identity
  completeness. Rotation refuses to prune active calls, and verification keeps
  evaluated state separate from completeness.

## Compatibility

- Current policy locks use format `6` and the immutable v0.9.6
  `schemas/v6/policy-lock.schema.json` identity.
- Supported legacy schema aliases and policy-lock formats 1 through 5 continue
  to migrate offline. Unknown URLs, crossed URL/format pairs, and future
  versions fail closed.
- Legacy top-level `mcp` authoring remains accepted during this compatibility
  window and preserves existing host behavior after canonical lowering.
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

Formats 1 through 5 migrate in memory. Run `reconc refresh .` when you
intentionally want the repository to persist the current format-6 lock and its
canonical action plan; review and commit policy source and lock together.
