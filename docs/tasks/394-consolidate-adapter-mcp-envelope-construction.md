# TASK 394: Consolidate adapter MCP envelope construction

## Why

Multiple adapters duplicate the same MCP envelope structs, outcome derivation, strict single-value JSON decoding, tool-name normalization, and event lookup maps. Copilot and Grok rebuild their immutable event tables for every hook event.

## Acceptance

- One internal envelope type and conversion path owns shared MCP fields and outcome semantics.
- One strict decoder helper owns single-value and trailing-data rejection while preserving adapter-specific types and diagnostics.
- Platform-specific validation and host contract differences remain explicit.
- Immutable event registries are constructed once and cannot be mutated by callers.
- Adapter parity tests and allocation benchmarks prove identical output with fewer per-event allocations.

## Sub-Tasks

- [ ] Diff every duplicated envelope and event contract before consolidation.
- [ ] Reuse the existing normalized envelope primitive where contracts are identical.
- [ ] Hoist immutable event tables and add mutation/parity regressions.
- [ ] Run focused adapter tests and benchmarks.

## Notes

- Verified from findings 50, 51, and 211.
- No abstraction may merge platform-specific fields or response semantics merely because names look similar.

## Deviations
