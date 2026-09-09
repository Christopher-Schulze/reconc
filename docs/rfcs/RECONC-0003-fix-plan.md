# RECONC-0003: Fix Plan

- Status: Frozen
- Producer: `reconc fix`
- Schema: `https://raw.githubusercontent.com/Christopher-Schulze/reconc/reconc-v0.9.9/schemas/v2/policy-fix-plan.schema.json`
- Format version: `2` (the v1 shape remains available through `BuildLegacyFixPlan`)

## Purpose

A fix plan turns a check report into ordered, deterministic remediation
steps. It never mutates the repository and never claims an autofix is
available. Agents use it to identify the shortest valid next action.

## Required Top-Level Fields

| Field | Type |
|---|---|
| `$schema` | string |
| `format_version` | string |
| `decision` | string |
| `summary` | string |
| `repo_root` | string |
| `lockfile_path` | string |
| `inputs` | object |
| `violation_count` | integer |
| `blocking_violation_count` | integer |
| `remediation_count` | integer |
| `remediations` | object array |

`remediation_count` must equal `len(remediations)`.

## Remediation Object

| Field | Type | Rule |
|---|---|---|
| `rule_id` | string | Source violation rule id. |
| `kind` | rule kind | Source violation kind. |
| `mode` | mode | Effective mode. |
| `priority` | string | `blocking` for block/fix modes, otherwise `non-blocking`. |
| `message` | string | Rule message. |
| `why` | string | Violation explanation. |
| `recommended_action` | string | Single recommended action. |
| `suggested_commands` | string array | Required commands for command rules. |
| `forbidden_commands` | string array | Matched or forbidden commands for forbid rules. |
| `suggested_claims` | string array | Required claims for claim rules. |
| `files_to_inspect` | string array | Rule source, matched paths, and required paths. |
| `steps` | string array | Ordered remediation steps. |
| `actions` | optional typed action array | Executable/evidence actions with explicit kind, arguments, working directory, authorization, and evidence requirements. |
| `source_path` | optional string | Rule authoring source. |
| `source_block_id` | optional string | Inline/preset block id. |
| `can_autofix` | boolean | Must be `false` in v2 and v1. |

## Typed Actions

`actions` is the machine execution contract for v2. `kind: "shell"` carries
the complete literal script in `shell`; consumers must not split or
interpolate it as argv. `kind: "argv"` carries already separated argument
values in `argv`. `authorization` and the `required_evidence`/
`required_claims` fields describe prerequisites and never grant permission.
Stable action codes include `run_command`, `assert_claim`, `provide_evidence`,
`refresh`, `retry`, `inspect`, and `request_approval`.
The text renderer presents the same action values and marks shell scripts as
literal shell operations.

## Next-Action Semantics

`reconc next` selects the highest-priority remediation and prints only the
next action. Blocking remediations outrank non-blocking ones. This is the
preferred agent path when a full plan would waste context.
