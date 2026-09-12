# TASK 550: Add native DeepSeek Harness integration

## Why

DSH is explicitly requested and absent from the platform registry. Its Claude/Codex bridges provide partial compatibility, but the inspected Claude bridge loses typed outcomes and continues after some configuration/hook failures. Native Cordis/tool-runtime integration offers a stronger path.

## Acceptance

- DSH is a first-class platform with generated installation artifacts, typed normalization, capability/status reporting, removal ownership, schemas, fixtures, and documentation.
- A native adapter gates supported tool execution through the existing Go engine and records truthful typed outcomes with repository/session/call binding.
- Missing decisions, worker errors, and late/short-circuited listeners cannot silently allow protected operations while the native enforcement extension is active.
- Native, nested/programmatic, subagent, cancellation, and multi-repository behavior have explicit tests and capability boundaries.
- The extension is loaded through a supported DSH profile mechanism without overwriting user profiles or adding a runtime dependency to the Reconc binary.
- A version-bound live result exists, or qualification remains explicitly blocked with the exact host limitation. A compatibility-only demo is not task completion.

## Sub-Tasks

- [ ] Prove the native extension and immutable execution guard design against pinned upstream.
- [ ] Add the DSH registry/generator/installer/normalizer and reuse the bounded worker transport.
- [ ] Integrate truthful outcomes, lifecycle, context injection, and profile ownership.
- [ ] Add generated-extension execution tests and isolated DSH integration tests.
- [ ] Propagate all platform references and qualify the installed DSH CLI.

## Technical Plan

1. Use upstream revision `c291e7961a515f6d7af9304e7fd1d257929aef26` as the initial source baseline, not an inferred stable release. The inspected root package declares `0.1.5-rc.2`. Inspect the installable CLI package and exact runtime package versions before choosing the supported pin.
2. Extend `internal/hooks/platforms.go`, generator/install/status/removal owners and the platform-name/host-event tests. Add narrowly scoped new `internal/hooks/dsh.go`, `internal/runtime/agentsession/dsh.go`, and their tests, following the existing OMP organization. Propagate enum/schema/CLI/bootstrap/reference consumers through a repository-wide reference search; do not hand-maintain a second platform list.
3. Generate a thin Cordis extension. The inspected signatures are: tools/pre-execute receives immutable ToolExecution and an asynchronous decision continuation; ToolRuntime.guard accepts a synchronous function returning a denial reason or undefined. Do not invent an asynchronous guard.
4. Proposed enforcement design: evaluate asynchronously through the existing Go worker in pre-execute; store the completed decision against the opaque execution token and immutable argument/repository identity. Register a synchronous monotonic final guard that denies governed operations when that decision is absent, stale, denied, or belongs to another execution. A listener that skips the rest of the pre waterfall must not bypass this final guard. Use bounded lifecycle-owned state or weak token ownership; clean up on result, cancellation, session disposal, and extension disposal. Prove this ordering with the actual ToolRuntime before integrating the product adapter.
5. Normalize callId/rootCallId, agent/session identity, CWD, AbortSignal lifecycle, direct file/shell operations, and MCP envelopes. Inspect the native tools' exact input/output schemas before alias mapping. Exercise nested programmatic calls and modes separately; raw arbitrary language execution is not automatically a parsed shell policy boundary.
6. Native tools/result observes a frozen final result after post-execute transformation. Distinguish that final host result from original process evidence. Post middleware can replace a successful value, so a success-shaped shell result alone cannot certify its original exit status. Use trusted tool-owned execution evidence where available; otherwise record observation and require the existing reconc exec/CI backstop for command proof. Never restore the bridge's text-only tool_response approach.
7. Establish policy/context before the first protected operation; detached session-start notification is insufficient. Use supported agent pre-step/turn lifecycle hooks for awaited setup, compaction reinjection, and bounded completion steering. Verify per-session versus per-turn scopes and subagent ownership; do not introduce unbounded stop loops.
8. Inspect apps/cli/src/profile-boot.ts and the actual profile format before generating an owned import/config fragment. Preserve unrelated Cordis plugins, existing profile code, and other repositories. Status must detect missing/disabled profile imports and duplicate bridge+native routes. User profile installation is an explicit integration action, not a side effect of skill installation.
9. Reuse the existing Bun test runtime and worker transport. Pin required upstream test packages only after package/license/size review; shipped Reconc remains a Go binary. If the pinned upstream offers no sufficient immutable guard/profile-loading contract, stop this implementation at that concrete blocker rather than shipping false native-enforcement claims.

## Verification

Run `go test ./internal/hooks ./internal/runtime/agentsession ./internal/cli ./internal/schema` plus real generated-extension execution using the pinned DSH ToolRuntime. Required controls: allowed/denied edits and shell, short-circuiting pre listener, worker crash/timeout, changed arguments, failed/canceled/transformed result, nested tool call, concurrent sessions/repositories, first-turn race, duplicate bridges, disabled extension, profile reload, stop bound, and disposal. Provider-backed DSH tests belong to TASK 555; offline runtime execution must not be mislabeled a live model test.

## Dependencies

TASK 545. Reuse verified transport/lifecycle improvements from TASK 549. TASK 551 owns shared skill discovery; TASK 553 owns compact guidance.

## Sources and Evidence

- [Native tool runtime and guard](https://github.com/deepseek-ai/deepseek-harness/blob/c291e7961a515f6d7af9304e7fd1d257929aef26/packages/core/tools/src/index.ts).
- [Agent lifecycle](https://github.com/deepseek-ai/deepseek-harness/blob/c291e7961a515f6d7af9304e7fd1d257929aef26/packages/core/agent/src/index.ts).
- [Claude compatibility bridge](https://github.com/deepseek-ai/deepseek-harness/blob/c291e7961a515f6d7af9304e7fd1d257929aef26/packages/hooks/hooks-claude-code/src/index.ts) and [configuration parsing](https://github.com/deepseek-ai/deepseek-harness/blob/c291e7961a515f6d7af9304e7fd1d257929aef26/packages/hooks/hooks-claude-code/src/config.ts).
- Sources inspected on 2026-09-12 and retained under `.build/planning-agent-integrations/dsh/`. No DSH executable was found on PATH.

## Notes

The native design is a technically grounded plan, not an implemented adapter. The synchronous guard constraint and transformed-result boundary are mandatory feasibility checks, not optional refinements.

## Deviations

None. No host or plugin was installed during planning.
