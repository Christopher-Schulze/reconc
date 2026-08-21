# RECONC-0001: Policy Lockfile

- Status: Frozen
- Contract: `.reconc/policy.lock.json`
- Schema: `https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.7/schemas/v6/policy-lock.schema.json`
- Format version: `6`

## Purpose

`reconc refresh` writes a deterministic lockfile that is the only
policy artifact trusted by runtime commands. `check`, `ci`, `explain`,
`fix`, `assert`, `can`, hooks, and agent-session gates must load this
file and reject it when schema, format, repo root, or source digest do
not satisfy the current portable envelope or source state.

## Source Inputs

Sources are loaded in precedence order:

1. optional global policy under `RECONC_HOME`
2. `CLAUDE.md`
3. `AGENTS.md`
4. `start.md`
5. inline fenced `reconc` / legacy policy blocks from agent context
6. compiler config `.reconc.yml` or `.reconc.yaml`
7. bundled or user presets named by `extends:`
8. policy files from configured `include:` patterns

The compiled `source_precedence` field is:

`global`, `claude_md`, `agents_md`, `start_md`, `inline_block`,
`compiler_config`, `preset`, `policy_file`

## Required Top-Level Fields

| Field | Type | Rule |
|---|---|---|
| `$schema` | string | Must equal the schema URL above unless `RECONC_SCHEMA_BASE_URL` deliberately rewrites the base. |
| `format_version` | string | Must equal `6`. |
| `compiler_version` | string | Build version that wrote the lockfile. |
| `repo_root` | string | Portable marker `.`. Physical checkout roots never enter current lockfiles. |
| `default_mode` | string | One of `observe`, `warn`, `block`, `fix`. |
| `rule_count` | integer | Must equal `len(rules)`. |
| `source_count` | integer | Must equal `len(sources)`. |
| `source_digest` | string | Lowercase SHA-256 hex of canonical source bundle. |
| `lock_digest` | string | Lowercase SHA-256 hex of the complete canonical lockfile payload with this field omitted. |
| `source_precedence` | string array | Ordered source-kind list. |
| `discovery` | object | Snapshot of discovery state and warnings. |
| `sources` | object array | Body-free provenance for every input source in precedence order. |
| `rules` | object array | Parsed, normalized, validated rules. |
| `actions` | object | Canonical action-plan format 1 with explicit defaults, canonical tools, and canonical rules. Legacy `mcp` authoring is lowered into this object. |

## Canonical Action Plan

Format 6 always contains exactly one `actions` runtime source of truth and
forbids a parallel top-level `mcp` object. The action plan carries its own
format version, frozen defaults, canonical tool declarations, canonical rules,
budgets, approvals, detectors, and ledger policy. Strict typed decoding, bounds,
selectors, effects, conditions, predicates, provenance, failure policy, cache
policy, and privacy selection are defined in RECONC-0008. Legacy `mcp` remains
accepted authoring input during its
compatibility window and is deterministically lowered before serialization.

## Source Digest

`source_digest` is SHA-256 over canonical JSON containing:

- `source_precedence`
- `sources`

The canonical JSON uses sorted object keys and no semantic dependence on
map iteration order. Recompiling identical sources must produce the same
digest and lockfile bytes.

Each current source record contains `kind`, a logical portable `path`, and
`content_sha256`. Optional `block_id` and positive `line_start` preserve
bounded provenance. Raw source content, physical checkout paths, and physical
global-policy paths are forbidden in the current lockfile.

## Lock Digest

`lock_digest` is SHA-256 over canonical JSON for every top-level field except
`lock_digest` itself. Runtime verifies it before using embedded rules. A current
format-6 lock then proves freshness by comparing its source count and
`source_digest` with one bounded load of the current source bundle. Runtime
strictly decodes repository rules and the canonical action contract into one
typed immutable plan. A migrated format-1, format-2, format-3, or format-4 lock
additionally re-parses current sources and requires equivalent canonical rule
and action behavior,
so an in-memory legacy migration cannot legitimize policy drift.

## Rule Entries

Every rule contains at least:

| Field | Type |
|---|---|
| `id` | non-empty unique string |
| `kind` | supported rule kind |
| `message` | non-empty string |
| `mode` | supported mode or inherited default |

Kind-specific fields are defined in `RECONC-0004`.

Optional fields include provenance (`source_path`, `source_block_id`),
deprecation metadata, monorepo scope metadata, evidence assertions,
composite checks, and script assertions. Unknown rule kinds must fail at
parse time, never degrade to pass.

## Runtime Loader Requirements

Runtime loaders must:

1. Refuse missing, malformed, stale, schema-drifted, or non-portable current
   lockfiles.
2. Validate registered legacy schema identities and migrate known format-1
   absolute-root, format-2 content-bearing, format-3, format-4, and format-5 lockfiles in
   memory to the current body-free `.` envelope without mutating the input.
3. Validate rule count and source count consistency.
4. Validate the complete `lock_digest`, then compare the current source-bundle
   identity with `source_digest` without reparsing a current format-6 lock.
5. For every migrated legacy lock, additionally validate exact embedded-rule
   plus canonical action parity with reparsed current sources.
6. Strictly decode one typed immutable runtime plan, reject unknown fields and
   unsupported shapes, and preserve declaration order through indexed subsets.
7. Treat generated lockfiles as generated output; users must re-run
   `reconc refresh` instead of editing them by hand.
