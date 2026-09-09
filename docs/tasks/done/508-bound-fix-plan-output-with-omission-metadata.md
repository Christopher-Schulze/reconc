# TASK 508: Bound fix-plan output with explicit omission metadata

## Why

The v2 fix-plan builder currently copies arbitrary report slices into JSON and
text output. A malformed or unusually large in-memory report can therefore
inflate remediation, action, and input arrays without telling an agent which
machine values were omitted. The review contract requires bounded output while
preserving every retained identifier byte-for-byte.

## Acceptance

- Current v2 fix plans bound remediation, input, hint, action, and nested action arrays with named constants.
- Retained strings and command arguments remain byte-exact; no semantic value is truncated.
- Omitted entries are reported in a typed `omissions` object, and `remediation_count` equals the emitted array length.
- Human text and v2 JSON render the same retained actions; v1 legacy JSON remains schema-compatible.
- Tests cover hostile cardinality, exact Unicode/whitespace/ shell values, deterministic map selection, schema validation, and bounded text output.

## Sub-Tasks

- [x] Define bounded v2 output limits and typed omission metadata without changing the v1 contract.
- [x] Apply limits at every fix-plan array boundary and preserve deterministic retained values.
- [x] Add regression/schema/text tests and update RFC and documentation.
- [x] Run focused tests and complete repository gates, archive this detail, and push one TASK commit.

## Notes

### Review provenance

- Follow-up to TASK 479's explicit technical-design requirement: bound input and field counts without shortening semantic values, with omission metadata when output cannot fit.
- This task is limited to fix-plan production and its published v2 contract; report/evaluator bounds remain owned by their existing contracts.

### Verification

- `go test ./internal/runtime ./internal/schema -count=1`, `go test ./... -count=1`, `make test-fast`, `make test`, `make vet`, `make lint`, `make self-host`, and `git diff --check` passed.
- The v2 schema digest is registered as `a5768cd1a35dd8046fc6b60b9f161b256517ed0096d154993fba98b22efb53c5`.

## Deviations

None planned.
