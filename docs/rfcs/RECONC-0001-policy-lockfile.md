# RECONC-0001: Policy Lockfile

- Status: Frozen
- Contract: `.reconc/policy.lock.json`
- Schema: `https://raw.githubusercontent.com/Christopher-Schulze/reconc/main/schemas/v2/policy-lock.schema.json`
- Format version: `2`

## Purpose

`reconc compile` writes a deterministic lockfile that is the only
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
| `format_version` | string | Must equal `2`. |
| `compiler_version` | string | Build version that wrote the lockfile. |
| `repo_root` | string | Portable marker `.`. Physical checkout roots never enter current lockfiles. |
| `default_mode` | string | One of `observe`, `warn`, `block`, `fix`. |
| `rule_count` | integer | Must equal `len(rules)`. |
| `source_count` | integer | Must equal `len(sources)`. |
| `source_digest` | string | Lowercase SHA-256 hex of canonical source bundle. |
| `lock_digest` | string | Lowercase SHA-256 hex of the complete canonical lockfile payload with this field omitted. |
| `source_precedence` | string array | Ordered source-kind list. |
| `discovery` | object | Snapshot of discovery state and warnings. |
| `sources` | object array | Every input source in precedence order. |
| `rules` | object array | Parsed, normalized, validated rules. |

## Source Digest

`source_digest` is SHA-256 over canonical JSON containing:

- `source_precedence`
- `sources`

The canonical JSON uses sorted object keys and no semantic dependence on
map iteration order. Recompiling identical sources must produce the same
digest and lockfile bytes.

## Lock Digest

`lock_digest` is SHA-256 over canonical JSON for every top-level field except
`lock_digest` itself. Runtime verifies it before using embedded rules. Runtime
also re-parses current policy sources and requires byte-equivalent canonical
rule payloads, so an in-memory legacy migration cannot legitimize rule drift.

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
2. Validate the immutable v1 schema identity and migrate known format-1
   absolute-root lockfiles in memory to the format-2 `.` envelope without
   mutating the input.
3. Validate rule count and source count consistency.
4. Validate `lock_digest` and exact embedded-rule parity with current sources.
5. Treat generated lockfiles as generated output; users must re-run
   `reconc compile` instead of editing them by hand.
