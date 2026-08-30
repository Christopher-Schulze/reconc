# TASK 400: Reject ambiguous YAML merge semantics

## Why

`yamlbound` returns both a raw `yaml.Node` and a decoded map. YAML merge keys are expanded in the decoded map but remain literal alias/merge nodes in the raw tree, so different policy consumers can assign different meanings to one admitted document.

## Acceptance

- One YAML document has one policy meaning across parser, ingest, template, and preset consumers.
- YAML merge keys and merge-tag aliases are rejected before either raw or decoded views are consumed.
- Bounds counting remains fail closed for aliases, expanded nodes, depth, and scalar bytes.
- Differential adversarial tests cover direct merge keys, nested aliases, explicit merge tags, duplicate keys, and every affected loader.

## Sub-Tasks

- [x] Inventory raw-node and decoded-map consumers and their unknown-field behavior.
- [x] Reject merge semantics in the bounded raw walk with precise errors.
- [x] Add cross-consumer differential fixtures and documentation.
- [x] Run focused YAML, parser, ingest, template, and preset tests.

## Notes

- Verified from finding 62.
- Supporting merge expansion would require a larger semantic redesign; explicit rejection is the surgical deterministic contract.
- Pre-fix differential tests proved that direct, nested, and explicitly tagged merge keys were expanded in decoded maps while remaining raw merge nodes; parser, ingest, and template consumers accepted them, while preset manifest decoding failed later with a different alias error.
- `yamlbound` now rejects exact YAML merge-key semantics during the bounded raw walk, before expanded walking or map decoding. Quoted literal `"<<"` keys remain ordinary strings, and existing duplicate-key, alias, expanded-node, depth, and scalar limits remain enforced.
- Differential regressions cover `yamlbound`, parser rule documents, ingest mappings, user templates, and preset manifests.
- Focused and full tests passed for `internal/yamlbound`, `internal/parser`, `internal/ingest`, `internal/templates`, and `internal/presets`.
- `make test-fast` passed.

## Deviations
