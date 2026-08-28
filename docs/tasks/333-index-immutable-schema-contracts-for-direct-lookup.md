# TASK 333: Index immutable schema contracts for direct lookup

## Why

Every schema lookup calls `contracts()`, which constructs the full contract slice, then scans it linearly and clones nested slices for a single result. Hot validation and rendering paths repeatedly rebuild static registry data.

## Acceptance

- Static contracts are initialized once and indexed by artifact, schema version, URL, alias, and format where required.
- Public accessors still return detached nested slices that callers cannot mutate.
- Deterministic order, registry validation, enterprise overrides, aliases, and compatibility observations remain unchanged.
- Allocation, mutation-isolation, schema, publication, and generated-reference tests pass.

## Sub-Tasks

- [ ] Measure registry construction and lookup allocation
- [ ] Build immutable ordered storage and exact indexes
- [ ] Preserve detached public snapshots
- [ ] Run schema and publication gates

## Notes

- Evidence: `internal/schema/registry.go:79-153,425+`.

## Deviations

None.
