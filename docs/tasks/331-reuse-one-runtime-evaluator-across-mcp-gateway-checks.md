# TASK 331: Reuse one runtime evaluator across MCP gateway checks

## Why

The MCP CLI policy loader constructs a new runtime evaluator for each snapshot refresh, and repository-effect evidence calls the package-level `CheckRepoPolicy`, which constructs another evaluator per tool call. Both discard the evaluator's plan cache.

## Acceptance

- One gateway-owned runtime evaluator serves policy refresh and repository-effect checks for its lifetime.
- Evidence evaluation reuses the already validated compiled policy where the trust boundary is identical.
- Source, lock, authority, repository identity, refresh generation, and concurrent mutation checks remain fail closed.
- Benchmarks prove reduced compile/source-load counts for read/write tools without stale-policy reuse.

## Sub-Tasks

- [ ] Thread one evaluator through gateway loader and evidence provider
- [ ] Expose only the minimum compiled check surface required
- [ ] Add refresh, mutation, concurrency, and call-count tests
- [ ] Run gateway, runtime, race, and benchmark gates

## Notes

- Evidence: `internal/cli/mcp_gateway_cmd.go:233-305` and `internal/runtime/evaluator_core.go:283-289`.

## Deviations

None.
