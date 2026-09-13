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

- [x] Compare installed OMP and pinned current upstream with the generated extension and normalizer.
- [x] Resolve proven input-order, outcome, and lifecycle gaps without duplicating policy logic.
- [x] Exercise the generated extension against real host contracts and adversarial extension ordering.
- [x] Update fixtures and documentation, then classify the OMP live result under TASK 545's proof rules.

## Technical Plan

1. Inspect `internal/hooks/omp.go`, `worker_client.go`, `internal/runtime/agentsession/omp.go`, worker CLI lifecycle, and existing Bun adapter/Go tests together. The generated TypeScript remains a thin adapter to the Go policy engine.
2. Current upstream tool_call supports blocking and input revision, and tool_result is a middleware chain. Test another extension revising a protected write after Reconc's decision and another transforming a failed result. Locate the real host dispatch/approval ordering before choosing the enforcement point. Do not claim extension-order immunity if the host exposes no final guard.
3. Normalize authoritative success/error and actual shell exit data from supported tools. Existing code examines details.exitCode for Bash and otherwise defaults success when isError is false; verify the host's real result shapes before declaring that path a defect. Never synthesize success from missing or rewritten evidence.
4. Preserve session and tool-call identities across retries, user_bash, cancellation, worker restart, and concurrent calls. Bound output/UTF-8 buffering and clean up owned workers on shutdown.
5. Keep session_stop as the completion boundary for the main agent, after background work settles. Current upstream caps continuations at eight; keep Reconc within host bounds. Do not use agent_end as veto or assume task/subagent stop has the main-agent contract.
6. Treat approval_requested/resolved as observations. Verify user_python and nested tool execution separately; do not add a speculative Python parser or claim arbitrary Python side effects are natively prevented.
7. Propagate source/version fixtures, generated extension tests, registry limitations, documentation, and skill guidance. Preserve host discovery toggles for shared skills; installation does not override disabled providers.

## Verification

The generated Bun adapter contracts and the uncached hook/session/CLI package suite passed. An installed OMP 18.1.18 run proved input replacement, final result transformation, generated-route liveness, and the ordering limit. Worker ownership, transport failure, cancellation, and background-start evidence passed adapter/runtime controls. Maximum Stop continuation and native policy denial were not exercised live; TASK 555 owns the full host qualification.

## Dependencies

TASK 545. Reuse lifecycle fixes with TASK 550 only when semantics actually match. Skill discovery is owned by TASK 551.

## Sources and Evidence

- [Pinned OMP 18.1.18 extension documentation](https://github.com/can1357/oh-my-pi/blob/00085d4e7dfdcfbf302c122fa2682b410a0f43d1/docs/extensions.md).
- [Native extension types](https://github.com/can1357/oh-my-pi/blob/00085d4e7dfdcfbf302c122fa2682b410a0f43d1/packages/coding-agent/src/extensibility/extensions/types.ts), [runner](https://github.com/can1357/oh-my-pi/blob/00085d4e7dfdcfbf302c122fa2682b410a0f43d1/packages/coding-agent/src/extensibility/extensions/runner.ts), and [agent loop](https://github.com/can1357/oh-my-pi/blob/00085d4e7dfdcfbf302c122fa2682b410a0f43d1/packages/agent/src/agent-loop.ts).
- Previous fixture: `18.0.11`, revision `b8ce33a58911c26bed1d84f0db9a5e2e727c49a2`. Installed binary and new fixture: `omp/18.1.18`, release tag revision `00085d4e7dfdcfbf302c122fa2682b410a0f43d1`.

## Notes

Ordering and outcome risks began as test hypotheses; the installed-host probes and source review below resolved them.

Entry: TASK 548 was pushed as `858f152b6df9c03b348f6b7a9160b195b074520b`, and the worktree was clean. Installed `omp/18.1.18` is the Homebrew release binary for upstream tag `v18.1.18` (tag ref `00085d4e7dfdcfbf302c122fa2682b410a0f43d1`). The tagged runner, shared event types, Bash implementation, agent loop, and extension docs were fetched read-only into a temporary directory for signature and dispatch review.

The tagged `ToolCallEventResult.input` contract permits later handlers to replace the tool's execution input; all handlers see the original input and the last replacement wins. The runner returns immediately on a blocking handler, but does not offer a final pre-execution callback after all revisions. Thus a later extension can replace an allowed command/write after Reconc's gate. The tagged `tool_result` runner chains `content`, `details`, and `isError` mutations in extension order; no final post-chain callback exists for an earlier extension. These are host ordering limitations that require adversarial proof and explicit scope, not an assumed Reconc bypass fix.

The tagged built-in Bash implementation returns a successful foreground result with `isError:false` and no `details.exitCode`, which explains the adapter's synthetic zero for a settled success. It also returns a background-start result with `details.async.state:"running"`, `isError:false`, and no exit code. The previous adapter treated that started job as command success; this task removed that positive-evidence defect. Completed background jobs settle through progress/delivery, not the initial `tool_result`.

The tagged wrapper passes the actually executed input to `tool_result`, but `tool_result.isError` is middleware state and can be changed by later extensions; it can also differ from a built-in tool's returned `result.isError`. The agent loop emits `tool_execution_end` after middleware with its final result and error status. The adapter now correlates those two events by native session/call identity, rejects ambiguous duplicate call IDs and unmatched/malformed end results, and forwards only the final event to Reconc. A background Bash start remains without command-success evidence even when the final event is non-error. The pre-action ordering limit remains; positive post evidence now names the actual executed input, but cannot retroactively block a later extension's replacement.

An OMP 18.1.18 print probe loaded two disposable extensions in order. Both pre handlers saw `printf ORIGINAL > observed.txt`; the later handler returned `printf REWRITTEN > observed.txt`, and the actual file contained `REWRITTEN`. The earlier post handler saw `isError:false`, the later handler changed it to true, and OMP's final `tool_execution_end` reported true. A second disposable repository was initialized with a source-built Reconc and its generated OMP extension. A successful Bash run emitted Reconc session start, pre-tool, final post-tool, Stop, and shutdown. A bounded adversarial run with Reconc before the rewriting/error-transforming extension emitted Reconc post-tool-failure for final failed Bash calls, while the rewritten file effect occurred. These are live route and host-order proofs on the installed binary; they do not prove Reconc blocked a native policy denial. The adversarial run hit OMP's 90-second limit after repeated model retries, so its overall session is not a positive completion control.

Closure: focused generated-extension and native-registry tests, the uncached hook/session/CLI package suite, `make test` (both complete uncached race modules, publication audit, reference docs, pack integrity, and release trust), `make vet`, `make lint`, and isolated `make self-host` passed. The OMP scaffold is generator-exact. Existing generated-extension tests cover worker isolation, shutdown, fail-closed timeout/spawn/invalid-output behavior, and aborted Stop; new tests cover actual-input/final-outcome correlation, ambiguous duplicate IDs, and background Bash evidence. The TASK 545 receipt rules still classify this OMP live exercise as route and host-order evidence, not full native enforcement qualification. TASK 555 owns the complete five-host live matrix.

## Deviations

The committed harness pack ZIP is deterministic binary output that cannot be edited surgically while preserving its byte-for-byte gate. A candidate pack was generated outside the repository and compared with the existing archive: only `manifest.json` and the OMP scaffold entry changed. The manifest was patched in place; the ZIP was refreshed through the repository's canonical pack builder after that exact diff review.
