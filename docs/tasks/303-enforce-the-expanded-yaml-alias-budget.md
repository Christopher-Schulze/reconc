# TASK 303: Enforce the expanded YAML alias budget

## Why

`yamlbound.walk` increments raw and expanded node counters identically, including alias traversal. `MaxNodes` always triggers before the larger `MaxExpandedNodes`, so the advertised independent expansion budget is unreachable.

## Acceptance

- Raw syntax nodes and recursively expanded alias nodes are counted by distinct definitions.
- Alias cycles, shared aliases, nested aliases, raw-node overflow, and expanded-node overflow produce deterministic separate failures.
- Expansion counting does not materialize an unbounded duplicate tree.
- Dedicated yamlbound unit, fuzz, and all YAML-consumer tests pass.

## Sub-Tasks

- [ ] Specify raw versus expanded node accounting
- [ ] Implement cycle-safe independent counters
- [ ] Add exact boundary and alias graph tests
- [ ] Run parser, template, preset, schema, and fuzz gates

## Notes

- Evidence: `internal/yamlbound/document.go:78-104`.

## Deviations

None.
