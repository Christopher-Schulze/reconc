# TASK 302: Route all untrusted YAML through bounded admission

## Why

Template and source-document loaders call `yaml.Unmarshal` directly, bypassing the shared depth, node, alias, scalar-byte, and single-document boundary.

## Acceptance

- Every user-controlled YAML entrypoint reaches `yamlbound` before materializing generic values.
- Empty, null, mapping-only, duplicate-document, alias, depth, node, and scalar contracts are consistent across policy, include, template, and preset inputs.
- Error types retain caller-specific context without exposing divergent parsers.
- Alias-bomb, maximum-input, compatibility, parser, template, and fuzz gates pass.

## Sub-Tasks

- [ ] Inventory direct YAML decoding in production paths
- [ ] Extend the bounded decoder only where caller shape requires it
- [ ] Migrate ingest and template loaders
- [ ] Add parity, bound, and fuzz coverage

## Notes

- Evidence: `internal/templates/loader.go:230-251` and `internal/ingest/source_loader.go:600-621`.

## Deviations

None.
