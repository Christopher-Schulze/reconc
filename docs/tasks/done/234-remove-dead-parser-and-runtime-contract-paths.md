# TASK 234: Remove dead parser and runtime contract paths

## Why

Four verified internal paths imply behavior that cannot occur or duplicate an
existing authority: `deny_write` explanation handles `requiredPaths` although
its callers never supply them; deterministic map-key collection reimplements
insertion sort despite an existing standard sort dependency; command-match
kind eligibility is checked before and inside its parser; and `policy.Check`
contains `BeforePaths`/`WhenPaths` fields that authoring rejects and sub-check
evaluation never populates. Removing these paths reduces the contract surface
without changing supported policy behavior.

## Acceptance

- `deny_write` explanation has one reachable recommendation path based on the
  rule's denied paths, with current text and ordering preserved.
- Deterministic string-set key ordering uses the existing standard-library sort
  path and retains nil/empty and stable output behavior.
- `command_match` eligibility and value validation have one owner; top-level
  and composite errors retain rule ID, kind, field, and source context.
- `policy.Check.BeforePaths` and `policy.Check.WhenPaths` are removed from the
  typed sub-check contract together with serializer, runtime validation,
  matcher collection, and tests that only preserve the dead fields.
- A complete schema and migration audit proves no accepted format-1-through-6
  check can contain those fields; legacy valid lockfiles continue to decode.
- Unknown lockfile fields remain rejected. Removing typed fields must not make
  malformed historical payloads silently acceptable.
- All production callers are searched before removal, and focused mutation
  tests demonstrate that each surviving validation path can fail.
- Parser, compiler, conflict, runtime-plan, composite, schema, fuzz, race, and
  portable-harness tests remain green with no output-contract drift.

## Sub-Tasks

- [x] Prove reachability and schema history for every removal target
- [x] Simplify deny-write explanation and deterministic key sorting
- [x] Consolidate command-match eligibility into one validator
- [x] Remove dead sub-check path fields across all typed boundaries
- [x] Add malformed-lock, error-context, output-parity, and mutation tests
- [x] Run parser/compiler/runtime/schema and portable verification

## Notes

- Session findings: `#7`, `#10`, `#12`, and `#14`.
- Primary code: `internal/runtime/evaluator.go`,
  `internal/parser/parser.go`, `internal/parser/unknown_fields.go`,
  `internal/policy/types.go`, `internal/compiler/compiler.go`, and
  `internal/runtime/runtime_plan.go`.
- This TASK removes only proven unreachable or duplicate paths. It does not add
  `require_read`, `couple_change`, nested composites, or new sub-check fields.
- Rule-level `BeforePaths` and `WhenPaths` are live public policy fields and
  must remain untouched.
- Repository-wide caller inspection proved the deny-write `requiredPaths`
  branch unreachable, the insertion sort redundant with the package's existing
  `sort` dependency, and both `Check` path-trigger fields absent from parser
  construction and composite evaluation. Rule-level path triggers remain
  unchanged.
- Command-match eligibility now has one owner in the rule/check field matrices;
  scalar parsing validates only exact/prefix values. Top-level and composite
  mutation tests prove rule ID, kind, field, source path, and check index remain
  present in diagnostics.
- Formats 1 and 4 historically exposed the two dead check properties, and the
  intermediate schemas inherited that shape. The runtime field contract never
  accepted them. A format-1-through-6 matrix now proves valid artifacts still
  build runtime plans while either dead field is rejected after migration.
  The current v6 schema now defines the exact `policy.Check` surface locally;
  immutable v1-through-v5 schema bytes and URLs were not changed. The updated
  v6 registry digest is
  `e54368e7c046303798ebab0bbbf3d16e4a68f4fdaeb3e98043b54ad5e64a08a4`.
- Verification: parser, compiler, runtime, and schema suites; their race tests;
  parser, runtime-rule, and migration fuzz runs; vet; publication audit;
  deterministic harness-pack check; and the portable harness race suite all
  passed.

## Deviations

None.
