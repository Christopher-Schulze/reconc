# TASK 302: Route all untrusted YAML through bounded admission

## Why

Template and source-document loaders call `yaml.Unmarshal` directly, bypassing the shared depth, node, alias, scalar-byte, and single-document boundary.

## Acceptance

- Every user-controlled YAML entrypoint reaches `yamlbound` before materializing generic values.
- Empty, null, mapping-only, duplicate-document, alias, depth, node, and scalar contracts are consistent across policy, include, template, and preset inputs.
- Error types retain caller-specific context without exposing divergent parsers.
- Alias-bomb, maximum-input, compatibility, parser, template, and fuzz gates pass.

## Sub-Tasks

- [x] Inventory direct YAML decoding in production paths
- [x] Extend the bounded decoder only where caller shape requires it
- [x] Migrate ingest and template loaders
- [x] Add parity, bound, and fuzz coverage

## Notes

- Evidence: `internal/templates/loader.go`, `internal/ingest/source_loader.go`, and `internal/cli/doctor_deep.go` directly materialized generic values from user-controlled YAML. Policy/schema and preset inputs already entered through `yamlbound`; assurance decoding only retypes an admitted mapping. Task-lifecycle and portable-harness decoders materialize typed values under separate byte and shape contracts, so they are not generic-decoding bypasses in this TASK.
- Verification: focused YAML consumer tests, `make test`, `make vet`, pinned Staticcheck v0.8.1 for both Go modules, and `make self-host` pass.

## Deviations

None.
