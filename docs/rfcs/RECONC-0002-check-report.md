# RECONC-0002: Check Report

- Status: Frozen
- Producers: `reconc check`, `reconc ci`, `reconc assert`, hooks
- Schema: `https://reconc.dev/schemas/policy-report/v1`
- Format version: `1`

## Purpose

A check report is the machine-readable decision artifact for runtime
evidence. It separates policy, evidence, and decision so agents,
hooks, CI, and humans can replay or render a result without asking an
LLM to interpret policy.

## Required Top-Level Fields

| Field | Type | Rule |
|---|---|---|
| `$schema` | string | Check-report schema URL. |
| `format_version` | string | Must equal `1`. |
| `ok` | boolean | `false` only when decision is `block`. |
| `repo_root` | string | Absolute repository root. |
| `lockfile_path` | string | Usually `.reconc/policy.lock.json`. |
| `decision` | string | `pass`, `warn`, or `block`. |
| `default_mode` | string | Lockfile default mode. |
| `summary` | string | One-line report summary. |
| `actions` | string array | One action per violation, index-aligned with `rule_ids`. |
| `rule_ids` | string array | Firing rule ids, index-aligned with `actions`. |
| `inputs` | object | Normalized evidence used for evaluation. |
| `violation_count` | integer | `len(violations)`. |
| `blocking_violation_count` | integer | Count of block/fix-mode violations. |
| `violations` | object array | Full violation records. |

`next_action` is optional and contains the first useful remediation
hint when one exists.

## Decision Logic

| Decision | Condition |
|---|---|
| `pass` | No warn/block/fix violations. |
| `warn` | At least one warn violation and no blocking violation. |
| `block` | At least one `block` or `fix` violation. |

`observe` mode never changes the decision.

## Inputs Object

The input object contains normalized runtime evidence:

| Field | Type |
|---|---|
| `read_paths` | string array |
| `write_paths` | string array |
| `commands` | string array |
| `command_results` | object array |
| `claims` | string array |

Paths must be repo-relative POSIX paths after normalization. Any path
that resolves outside the repository root must fail before evaluation.

## Violation Object

| Field | Type |
|---|---|
| `rule_id` | string |
| `kind` | rule kind |
| `mode` | effective mode |
| `message` | string |
| `explanation` | string |
| `recommended_action` | string |
| `matched_paths` | string array |
| `matched_commands` | string array |
| `matched_claims` | string array |
| `required_paths` | string array |
| `required_commands` | string array |
| `required_claims` | string array |
| `source_path` | optional string |
| `source_block_id` | optional string |

Empty arrays must remain arrays. Producers should prefer stable
ordering and deterministic wording where practical, but JSON shape is
the contract.

## Terse Report

`--terse` may emit a compact object with only:

- `decision`
- `ok`
- `rule_ids`
- `actions`

This is a derived view of the same decision, intended for hot agent
loops.
