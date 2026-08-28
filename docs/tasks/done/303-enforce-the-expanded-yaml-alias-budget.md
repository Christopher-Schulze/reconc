# TASK 303: Enforce the expanded YAML alias budget

## Why

`yamlbound.walk` increments raw and expanded node counters identically, including alias traversal. `MaxNodes` always triggers before the larger `MaxExpandedNodes`, so the advertised independent expansion budget is unreachable.

## Acceptance

- Raw syntax nodes and recursively expanded alias nodes are counted by distinct definitions.
- Alias cycles, shared aliases, nested aliases, raw-node overflow, and expanded-node overflow produce deterministic separate failures.
- Expansion counting does not materialize an unbounded duplicate tree.
- Dedicated yamlbound unit, fuzz, and all YAML-consumer tests pass.

## Sub-Tasks

- [x] Specify raw versus expanded node accounting
- [x] Implement cycle-safe independent counters
- [x] Add exact boundary and alias graph tests
- [x] Run parser, template, preset, schema, and fuzz gates

## Notes

- Evidence: `internal/yamlbound/document.go:78-104`.
- Contract: raw admission counts each syntax node once without following alias pointers; expanded admission replaces each alias occurrence with its target graph, counts shared targets per occurrence, and never materializes a duplicate tree. Both forms enforce depth independently; decoded scalar bytes follow the expanded view.
- Verification: exact raw/expanded boundary and alias-graph tests, all YAML consumers, `make test`, `make vet`, pinned Staticcheck v0.8.1, and `make self-host` pass.

## Deviations

None.
