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

- [x] Diff every duplicated envelope and event contract before consolidation.
- [x] Reuse the existing normalized envelope primitive where contracts are identical.
- [x] Hoist immutable event tables and add mutation/parity regressions.
- [x] Run focused adapter tests and benchmarks.

## Notes

- Verified from findings 50, 51, and 211.
- No abstraction may merge platform-specific fields or response semantics merely because names look similar.
- Current-code proof: Pi, Oh My Pi, and ZCode each declared the same six-field MCP envelope and independently derived the same pre/success/failure state; Pi, Oh My Pi, ZCode, Kimi Code, and Copilot duplicated strict single-value decoding; Copilot, Grok, and Devin constructed event maps on every validation while Pi, Oh My Pi, ZCode, and Kimi exposed mutable package maps.
- The implementation retains platform-local payload validation and diagnostics, but routes identical MCP serialization, outcome derivation, strict JSON decoding, Pi/OMP tool normalization, and native event lookup through shared internal primitives. Registry snapshots are copies, so callers cannot mutate the construction-once tables.
- Focused normalization/parity/mutation tests, the complete `internal/runtime/agentsession` package, and `make test-fast` passed. The Copilot event benchmark measured the shared registry at 0 B/op and 0 allocs/op versus the former per-event table at 952 B/op and 15 allocs/op across three repeated samples.

## Deviations
