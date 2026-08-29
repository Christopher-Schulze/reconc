# TASK 400: Reject ambiguous YAML merge semantics

## Why

`yamlbound` returns both a raw `yaml.Node` and a decoded map. YAML merge keys are expanded in the decoded map but remain literal alias/merge nodes in the raw tree, so different policy consumers can assign different meanings to one admitted document.

## Acceptance

- One YAML document has one policy meaning across parser, ingest, template, and preset consumers.
- YAML merge keys and merge-tag aliases are rejected before either raw or decoded views are consumed.
- Bounds counting remains fail closed for aliases, expanded nodes, depth, and scalar bytes.
- Differential adversarial tests cover direct merge keys, nested aliases, explicit merge tags, duplicate keys, and every affected loader.

## Sub-Tasks

- [ ] Inventory raw-node and decoded-map consumers and their unknown-field behavior.
- [ ] Reject merge semantics in the bounded raw walk with precise errors.
- [ ] Add cross-consumer differential fixtures and documentation.
- [ ] Run focused YAML, parser, ingest, template, and preset tests.

## Notes

- Verified from finding 62.
- Supporting merge expansion would require a larger semantic redesign; explicit rejection is the surgical deterministic contract.

## Deviations
