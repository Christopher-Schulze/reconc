# TASK 333: Index immutable schema contracts for direct lookup

## Why

Every schema lookup calls `contracts()`, which constructs the full contract slice, then scans it linearly and clones nested slices for a single result. Hot validation and rendering paths repeatedly rebuild static registry data.

## Acceptance

- Static contracts are initialized once and indexed by artifact, schema version, URL, alias, and format where required.
- Public accessors still return detached nested slices that callers cannot mutate.
- Deterministic order, registry validation, enterprise overrides, aliases, and compatibility observations remain unchanged.
- Allocation, mutation-isolation, schema, publication, and generated-reference tests pass.

## Sub-Tasks

- [x] Measure registry construction and lookup allocation
- [x] Build immutable ordered storage and exact indexes
- [x] Preserve detached public snapshots
- [x] Run schema and publication gates

## Notes

- `staticRegistry` builds the existing ordered contract and observation literals once.
- Direct indexes cover artifact membership, current contract, artifact/version, canonical and alias identities, enterprise paths, and contract formats; public contracts still clone nested slices.
- `go test ./internal/schema -count=1` passed.
- `go test ./internal/schema -run '^$' -bench 'BenchmarkRegistry' -benchtime=100x -count=1` passed: direct lookups measured 55-308 ns/op; one-time builder measured 17.9 us/op and 40,520 B/op when explicitly rebuilt.
- Evidence source: `internal/schema/registry.go:79-184,453-487`.

## Deviations

None.
