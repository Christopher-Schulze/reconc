# TASK 204: Reject fields unsupported by a rule kind

## Why

Parser unknown-field validation uses the union of fields accepted by all rule
kinds. Required-field checks then ensure fields are present but generally do
not reject valid global fields that are meaningless for the selected kind.
Configurations such as a deny-write rule carrying claim-only fields can be
accepted while the evaluator ignores those fields, creating misleading policy
authorship.

## Acceptance

- Every rule kind has a canonical allowlist of top-level fields and nested
  check/evidence fields, including common metadata explicitly shared by kinds.
- Any unsupported field fails compilation with rule ID, kind, field, and source
  location; empty values do not bypass validation.
- Templates are validated after merge against the resolved rule kind, so they
  cannot inject ignored fields.
- Schemas, parser behavior, examples, docs, and generated policy references use
  the same field matrix.
- Table-driven tests cover every known field against every rule kind and fail
  when a new field lacks an explicit classification.

## Sub-Tasks

- [x] Build the canonical rule-kind field matrix
- [x] Enforce it after template expansion and before compilation
- [x] Align nested objects, schema, documentation, and examples
- [x] Add exhaustive matrix and regression tests
- [x] Run parser, template, compiler, and complete gates

## Notes

- Verified around union unknown-field handling and required-field checks in
  `internal/parser/parser.go`.

## Deviations

None.
