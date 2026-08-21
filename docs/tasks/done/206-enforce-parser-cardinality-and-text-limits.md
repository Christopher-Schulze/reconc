# TASK 206: Enforce parser cardinality and text limits

## Why

Ingest bounds source and bundle bytes, and lockfile publication has a final
size cap, but parser collections and individual strings do not consistently
enforce early limits. A bounded YAML bundle can still decode into very large
rule lists, command/path lists, or messages and make validation, matching, and
serialization perform disproportionate work before the late lockfile limit.

## Acceptance

- Canonical limits exist for rule count, checks per rule, list items, pattern
  bytes, command bytes, message bytes, nesting depth, and total decoded scalar
  bytes, aligned with runtime and schema limits.
- Limits are checked as early as the decoder/parser can prove them and errors
  include source path, block, rule, field, actual value, and allowed maximum.
- YAML aliases/anchors, duplicate keys, and nested collections cannot amplify a
  bounded source into unbounded retained state.
- Boundary tests cover limit-1, limit, limit+1 and combined-limit cases; fuzz and
  memory tests establish bounded behavior.
- Existing valid maximum fixtures still compile and evaluate.

## Sub-Tasks

- [x] Inventory existing ingest, schema, compiler, and runtime bounds
- [x] Define one coherent parser resource-limit contract
- [x] Enforce limits during decode and typed rule construction
- [x] Add boundary, amplification, fuzz, and memory tests
- [x] Run parser, compiler, runtime, and complete gates

## Notes

- The parser now decodes exactly one YAML document through a `yaml.Node` walk
  before typed allocation. It bounds depth, node count, alias count, alias
  expansion, and aggregate scalar bytes, then applies the same item and text
  ceilings to top-level/scoped rules, composite checks, evidence, required
  files, assurance gates, and nested string collections.
- Limit errors identify the source path and block, rule ID and field when
  available, plus actual and maximum values. Duplicate keys, recursive aliases,
  trailing documents, and malformed YAML remain fail-closed parser errors.
- Focused parser tests cover limit+1 failures, accepted limit-1/limit message
  boundaries, alias amplification, duplicate keys, and a fuzz entry point.
  Fresh final candidate verification in TASK 221 passed the complete root and
  portable-template race gates.

## Deviations

None.
