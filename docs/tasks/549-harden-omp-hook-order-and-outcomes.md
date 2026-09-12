# TASK 549: Harden OMP hook order and outcomes

## Why

The OMP fixture targets 18.0.11 while the installed CLI is 18.1.18. Native extensions can revise inputs and transform tool results, so extension ordering and authoritative outcomes need a fresh check. Stop handling must respect background execution and host continuation bounds.

## Acceptance

- The supported OMP version/source and native event signatures are pinned and tested.
- Policy applies to the arguments that execute, or a reproducible host limitation is reported explicitly.
- Failed/canceled tools cannot create successful evidence after result transformation.
- Stop, background tasks, worker cancellation/restart, and shutdown remain bounded with no orphan process or session cross-talk.
- Approval observations and Python entrypoints are described according to their real enforcement boundaries.

## Sub-Tasks

- [ ] Compare installed OMP and pinned current upstream with the generated extension and normalizer.
- [ ] Resolve proven input-order, outcome, and lifecycle gaps without duplicating policy logic.
- [ ] Exercise the generated extension against real host contracts and adversarial extension ordering.
- [ ] Update fixtures and documentation, then qualify OMP with TASK 545.

## Technical Plan

1. Inspect `internal/hooks/omp.go`, `worker_client.go`, `internal/runtime/agentsession/omp.go`, worker CLI lifecycle, and existing Bun adapter/Go tests together. The generated TypeScript remains a thin adapter to the Go policy engine.
2. Current upstream tool_call supports blocking and input revision, and tool_result is a middleware chain. Test another extension revising a protected write after Reconc's decision and another transforming a failed result. Locate the real host dispatch/approval ordering before choosing the enforcement point. Do not claim extension-order immunity if the host exposes no final guard.
3. Normalize authoritative success/error and actual shell exit data from supported tools. Existing code examines details.exitCode for Bash and otherwise defaults success when isError is false; verify the host's real result shapes before declaring that path a defect. Never synthesize success from missing or rewritten evidence.
4. Preserve session and tool-call identities across retries, user_bash, cancellation, worker restart, and concurrent calls. Bound output/UTF-8 buffering and clean up owned workers on shutdown.
5. Keep session_stop as the completion boundary for the main agent, after background work settles. Current upstream caps continuations at eight; keep Reconc within host bounds. Do not use agent_end as veto or assume task/subagent stop has the main-agent contract.
6. Treat approval_requested/resolved as observations. Verify user_python and nested tool execution separately; do not add a speculative Python parser or claim arbitrary Python side effects are natively prevented.
7. Propagate source/version fixtures, generated extension tests, registry limitations, documentation, and skill guidance. Preserve host discovery toggles for shared skills; installation does not override disabled providers.

## Verification

Run `go test ./internal/hooks ./internal/runtime/agentsession ./internal/cli`, including the actual generated Bun adapter contracts. Add real execution controls for input rewrite ordering, nonzero Bash, canceled jobs, malformed response, background completion, maximum stop continuation, and worker death. Report live qualification separately from adapter execution.

## Dependencies

TASK 545. Reuse lifecycle fixes with TASK 550 only when semantics actually match. Skill discovery is owned by TASK 551.

## Sources and Evidence

- [Pinned OMP extension documentation](https://github.com/can1357/oh-my-pi/blob/ae8ba8b357a9e70bf6281de8c37d7888b6e4b779/docs/extensions.md).
- [Native extension types](https://github.com/can1357/oh-my-pi/blob/ae8ba8b357a9e70bf6281de8c37d7888b6e4b779/packages/coding-agent/src/extensibility/extensions/types.ts) and [runner](https://github.com/can1357/oh-my-pi/blob/ae8ba8b357a9e70bf6281de8c37d7888b6e4b779/packages/coding-agent/src/extensibility/extensions/runner.ts).
- Current fixture: `18.0.11`, revision `b8ce33a58911c26bed1d84f0db9a5e2e727c49a2`. Local binary: `omp/18.1.18`. Upstream HEAD above was inspected on 2026-09-12; equivalence to the installed build has not been assumed.

## Notes

Ordering and outcome risks are explicit test hypotheses, not already demonstrated exploits.

## Deviations

None.
