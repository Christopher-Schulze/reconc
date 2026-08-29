# TASK 331: Reuse one runtime evaluator across MCP gateway checks

## Why

The MCP CLI policy loader constructs a new runtime evaluator for each snapshot refresh, and repository-effect evidence calls the package-level `CheckRepoPolicy`, which constructs another evaluator per tool call. Both discard the evaluator's plan cache.

## Acceptance

- One gateway-owned runtime evaluator serves policy refresh and repository-effect checks for its lifetime.
- Evidence evaluation reuses the already validated compiled policy where the trust boundary is identical.
- Source, lock, authority, repository identity, refresh generation, and concurrent mutation checks remain fail closed.
- Benchmarks prove reduced compile/source-load counts for read/write tools without stale-policy reuse.

## Sub-Tasks

- [x] Thread one evaluator through gateway loader and evidence provider
- [x] Expose only the minimum compiled check surface required
- [x] Add refresh, mutation, concurrency, and call-count tests
- [x] Run gateway, runtime, race, and benchmark gates

## Notes

- Evidence: `internal/cli/mcp_gateway_cmd.go:233-305` and `internal/runtime/evaluator_core.go:283-289`.
- `gatewayConfig` now creates one `runtime.Evaluator` and passes the same owner to
  the policy loader and evidence provider. The loader retains its
  freshness-checked plan cache across startup and refresh snapshots.
- `PolicySnapshot` carries only a narrow `RepositoryEffectCheck` callback. The
  CLI binds it to the validated `CompiledPolicyEvaluator`; repository-effect
  evidence therefore evaluates that immutable plan without a second lockfile or
  source compilation. A strict evaluator is still required for the fallback
  path, and gateway freshness, authority, repository identity, generation, and
  pre-dispatch resampling remain unchanged.
- Tests cover shared evaluator ownership, immutable plan reuse, policy mutation
  rejection, concurrent repository-effect checks, and cancellation. The
  repository-effect benchmark reports `0 plan-rebuilds/op` for shared read and
  write snapshots versus `1.000 plan-rebuilds/op` when a new evaluator is used
  for each call. `go test` and the focused race suites for `cli`, `mcpgateway`,
  and `runtime` are green.

## Deviations

None.
