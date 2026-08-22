# RECONC-0004: Rule Kinds

- Status: Frozen
- Contract: parsed rule semantics

## Modes

| Mode | Effect |
|---|---|
| `observe` | Records only; never changes decision. |
| `warn` | Non-blocking violation; decision may become `warn`. |
| `block` | Blocking violation; decision becomes `block`. |
| `fix` | Blocking violation with remediation emphasis; decision becomes `block`. |

## Core Rule Kinds

| Kind | Required fields | Semantics |
|---|---|---|
| `deny_write` | `paths` | Fails when any write path matches protected paths. |
| `require_read` | `paths`, `before_paths` | Writes matching `paths` require a prior read matching `before_paths`. |
| `require_command` | `commands`, `when_paths` | Writes matching `when_paths` require at least one command string. |
| `require_command_success` | `commands`, `when_paths` | Like `require_command`, but success must be present in command results. |
| `forbid_command` | `commands` | Fails when a command matches forbidden command patterns. |
| `couple_change` | `paths`, `when_paths` | A primary write matching `paths` requires a separate companion write matching `when_paths`; a path matching both is classified as a companion. |
| `require_claim` | `claims`, `when_paths` | Writes matching `when_paths` require at least one listed claim. |

Every string-list entry is trimmed during parsing and must remain non-empty.
This applies uniformly to paths, commands, claims, script arguments,
`cache_inputs`, and evidence `must_contain` values. Static duplicate detection
is order-insensitive over these normalized values. Two `require_read` rules are
duplicates only when both their governed `paths` and prerequisite
`before_paths` lists match; trigger-only or absent fields never establish a
duplicate.

The command kinds (`require_command`, `require_command_success`,
`forbid_command`) accept an optional additive `command_match` field:
`exact` (default) compares normalized whole strings; `prefix` also
matches a recorded command that extends an expected command at a token
boundary (`pip install` matches `pip install requests`, never
`pip installer`). For `require_command_success`, prefix mode also
accepts runs carrying extra arguments (such as a `-run` filter), so
authors opt in deliberately. Composite sub-checks carry the field on
the check entry.

## Evidence Rule Kinds

| Kind | Required fields | Semantics |
|---|---|---|
| `require_fresh_file` | `required_files`, `when_paths` | Writes matching `when_paths` require listed files to exist and optionally be fresh by `max_age_hours`. |
| `require_evidence` | `evidence`, `when_paths` | Writes matching `when_paths` require evidence files to satisfy existence/content/line-count assertions. |
| `require_script` | `script`, `when_paths` | Writes matching `when_paths` require a repo-local policy-authored script to return success. |
| `require_assurance` | `assurance`, `when_paths` | Writes matching `when_paths` require every applicable typed native assurance gate to pass. |

`require_script` may use `args`, `timeout_sec`, `kill_timeout_sec`, and
`cache_inputs`. Script paths must be repo-relative and must not
escape the repository root. `cache_inputs` names the literal repo-relative
paths the script reads, each a file or a directory; globs, template variables,
escaping paths, and duplicates are invalid. Stop report reuse binds those paths
and never reuses a report for a script that declares none. A policy-authored
script is trusted repository code, not sandboxed input. Reconc filters its
environment to reduce incidental secret exposure, but this is secret
minimization rather than process isolation. `HOME` remains available because
common toolchains need user-level caches and configuration; policy authors must
therefore treat user configuration reachable through `HOME` as visible to the
script. Payload-provided command strings are never
executed as scripts. Only `status=pass` with exit code 0 passes. Exit code 2 is
a policy block. Timeout, launch or process failure, any other exit, and an
unknown or contradictory status fail closed. Timeout diagnostics identify the
effective configured duration and matched write path without relying on partial
script output; composite checks and batched-audit fallback use the same result
contract. Batched audit output must contain each requested mode exactly once,
must contain no unrequested mode, and must agree with the process disposition:
exit zero cannot report failures and exit two must report at least one failure.
Malformed or incomplete structured output falls back to independent rule
execution; a valid but contradictory process/result pair blocks every rule in
that batch.

`require_assurance` performs no network calls and spawns no subprocesses. Its
typed gates are `repository_layout`, `generated_reference`,
`language_boundary`, `dependency_pins`, `package_scripts`, `network_boundary`,
`process_boundary`, `substantive_proof`, `live_verification`,
`go_concurrency_boundary`, `go_format`, and `source_hygiene`. Fields irrelevant
to a selected gate type are invalid. Source gates are changed-file scoped;
repository-layout and substantive-proof gates inspect their full configured
authority surface. `package_scripts` checks only non-empty scripts declared in
the selected package manifests and accepts matching recorded package-manager
evidence; a configured manager that is missing, mismatched, or ambiguous fails
explicitly. `dependency_pins` and `package_scripts` share the same package JSON
reader and accept at most one leading UTF-8 BOM. Every `applicable_if` pattern
is validated before matching. Command-evidence gates accept only `all` or `any`
in compiled state. Substantive proof authoring defaults omitted
`max_age_hours` to 24 and treats explicit zero as no staleness limit while still
rejecting invalid or future-dated timestamps. Assurance never executes a package manager.
Operational read and scan limits fail closed.

## Composite Rule Kinds

| Kind | Required fields | Semantics |
|---|---|---|
| `all_of` | `checks`, `when_paths` | All sub-checks must pass. |
| `any_of` | `checks`, `when_paths` | At least one sub-check must pass. |
| `not` | exactly one `checks` entry, `when_paths` | The sub-check must fail. |

Composite checks use inline form and inherit the parent rule's mode,
message, scope, and `when_paths` capture context.

## Monorepo Scopes

Rules may be declared inside `scopes:` blocks. Scoped rules receive
`scope_paths` and `scope_id` metadata and only evaluate when input
paths intersect that scope.

## Template Variables

Path patterns may capture template variables such as `{task_id}`.
Those variables can be substituted into required files, evidence
files, script args, and composite sub-checks. If a write path does not
produce the capture required by a dependent check, the check must fail
closed.

## Claims

Claims are explicit opaque strings. `reconc` checks whether a claim was
asserted; it does not verify that the external fact is true. Agents
must only pass claims that actually happened in the session or CI run.

## Action Policy Rules

Format-5 policy locks also contain the separate canonical `actions` plan from
RECONC-0008. Action rules use transport/tool selectors, action phases, typed
conditions, and action decisions; they are not repository rule kinds and never
enter the `rules` array described above. Legacy `mcp` authoring lowers only into
that action plan, so repository-rule dispatch and action-policy dispatch have
one unambiguous owner each.
