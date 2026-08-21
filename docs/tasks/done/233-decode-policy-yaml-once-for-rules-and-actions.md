# TASK 233: Decode policy YAML once for rules and actions

## Why

`ParseRuleDocuments` decodes each rule-bearing YAML source into a generic
mapping, then `parseActionPolicy` decodes the same source bytes again into a
`yaml.Node` when the source can carry `actions`. The action parser needs node
tags and positions for strict canonical scalar validation, while rule parsing
needs the generic mapping. Both views should come from one bounded syntax tree
so duplicate-key, alias, depth, cardinality, and trailing-document behavior
cannot drift and configuration refresh does not pay for two YAML parses.

## Acceptance

- Every rule-bearing source is syntactically decoded at most once per
  `ParseRuleDocuments` transaction, including compiler config and impact
  candidate sources that contain actions.
- One source-document representation retains the validated root `yaml.Node`
  plus the generic mapping or derives both without reparsing source text.
- Action canonical integer, boolean, string, alias, duplicate-key, and source
  context behavior remains exact; conversion to `interface{}` must not erase
  tags before action validation.
- Existing parser limits for bytes, YAML depth, nodes, aliases, expanded nodes,
  scalar bytes, rule counts, actions, and trailing documents remain enforced
  once at the authoritative boundary.
- Rule, scope, MCP compatibility, action, and default-mode parsing retain
  deterministic ordering and current typed error classes.
- Differential tests compare the previous two-decoder behavior over valid and
  invalid corpora; fuzz tests assert rule/action output and error-kind parity.
- An instrumented benchmark proves one YAML decode for mixed rules/actions and
  reports latency, bytes, and allocations for small and maximum-legal inputs.
- Ingest's separate repository-config parse ownership from TASK 194 is not
  duplicated or regressed.

## Sub-Tasks

- [x] Design one bounded parser source-document representation
- [x] Decode the YAML node once and derive the rule mapping safely
- [x] Convert action parsing to consume the shared node
- [x] Add differential, tag, duplicate-key, limit, and fuzz regressions
- [x] Benchmark mixed documents and run parser/compiler/runtime gates

## Notes

- Session finding: `#11`.
- Primary code: `internal/parser/parser.go`, `internal/parser/action.go`, and
  `internal/parser/limits.go`.
- Do not simplify action parsing to generic `map[string]interface{}` values;
  exact YAML tags are part of canonical numeric and boolean validation.
- This TASK removes duplicate syntax decoding only. Independent typed rule and
  action validation phases remain appropriate.
- `parserSourceDocument` now owns the one bounded `yaml.Node` plus its decoded
  mapping. Compiler and impact-candidate action parsing consumes that retained
  root, so action tags and aliases are validated without reopening source
  bytes. Non-action sources retain the historical whitespace normalization;
  action-capable sources retain the stricter historical raw-byte syntax
  boundary.
- An injected transaction-local decoder proves one call for each compiler,
  impact-candidate, and ordinary policy source while context-only prose remains
  skipped. The differential corpus and action fuzz target compare the shared
  representation with a test-only copy of the former two-pass pipeline for
  output acceptance and typed error-kind parity. Fuzzing found and retained
  raw control-byte and malformed UTF-8 seeds.
- The instrumented Apple M1 benchmark, ten iterations, measured approximately
  143 us, 18.1 KB, and 286 allocations for a small mixed document, and 50.2 ms,
  32.6 MB, and 773,742 allocations for a maximum-legal 4,096-rule mixed
  document. Each iteration asserted exactly one YAML syntax decode.
- Verification: full parser tests, parser race tests, a five-second
  differential action fuzz run, compiler and runtime tests, `go vet` for all
  three packages, and the small/maximum-legal benchmark passed.

## Deviations

None.
