# TASK 546: Qualify current Codex hook contract

## Why

The Codex adapter is pinned to an older source contract. Current documentation includes Interrupt and a broader local-function hook surface than the generated matchers cover. Unsupported response fields can also change host behavior.

## Acceptance

- An explicit version-qualified event/tool inventory accounts for current Codex CLI behavior and the existing compatibility baseline.
- Supported policy-relevant tool routes are normalized and gated; excluded hosted/transport routes are named honestly.
- Interrupt, permissions, async command completion, compaction, and subagent behavior have real behavioral tests and bounded lifecycle handling.
- Generated configuration is discoverable under the tested trust/config layers, and live enforcement is reported only with TASK 545 evidence.
- Existing Codex behavior and other platforms retain their guarantees.

## Sub-Tasks

- [ ] Pin the actual supported Codex source/version and compare its event signatures with the registry and generated configuration.
- [ ] Implement the verified event, matcher, response, and lifecycle changes with compatible normalization.
- [ ] Add generated-config and runtime regressions, including deny-response negative controls.
- [ ] Update versioned fixtures and integration docs; qualify the CLI using TASK 545.

## Technical Plan

1. Read `internal/hooks/platforms.go`, the Codex generator in `generators.go`, `codex_activation*.go`, `internal/runtime/agentsession` normalization/permission/compaction handlers, and `internal/hooks/testdata/host-events/codex.json` together. Keep the registry as the capability owner.
2. Compare installed `codex-cli 0.154.0` with the pinned source that actually produced that build. Current upstream HEAD is not automatically its contract. The existing event fixture references `45f8cafa4e2ec20f9b189d5aa9409e424b6d3d09`.
3. Current official hooks documentation exposes Interrupt with a short deadline and no veto/restart semantics; most local function tools now participate in pre/post hooks. Inventory exact tool names and aliases before widening `Write|Edit|MultiEdit|Bash|apply_patch`. Handle code-mode inner calls and asynchronous execution completion without counting polling as a second execution.
4. Route Interrupt to bounded cancellation/lifecycle observation, never completion approval. Keep SessionEnd cleanup separate. Verify compaction reinjection and child-session identity against TASK 553.
5. Emit only response fields accepted for the particular event: valid PreToolUse denial, nested PermissionRequest decisions, and the supported context shape. Test fields the host rejects rather than assuming Claude compatibility. Ensure MCP permission/pre routes use the existing classified MCP policy model.
6. Verify `.codex/hooks.json` plus `.codex/config.toml` activation against layered config, trusted/untrusted repositories, and managed-only hooks. Report configuration suppression as unavailable, not loaded.
7. Propagate generated artifacts, status, schemas, host fixture, `docs/documentation.md`, `internal/agentguide/guide.md`, and the portable platform reference. Preserve the CLI/Git/CI fallback and explicitly identify hosted tools that lack hooks.

## Verification

Run `go test ./internal/hooks ./internal/runtime/agentsession ./internal/cli` and the existing generated-hook integration tests. Exercise valid/malformed deny JSON, nonzero execution, blocked edit/apply_patch, supported local-function variants, MCP denial, repeated polling, cancellation, compaction, and child cleanup. The final live matrix must contain the exact Codex executable and tested modes; an upstream document alone is not a passed test.

## Dependencies

TASK 545. Coordinate briefing reinjection with TASK 553; TASK 555 owns final combined qualification.

## Sources and Evidence

- [Official Codex hook contract](https://learn.chatgpt.com/docs/hooks), checked 2026-09-12.
- [Existing pinned source contract](https://github.com/openai/codex/tree/45f8cafa4e2ec20f9b189d5aa9409e424b6d3d09/codex-rs).
- Local generated matcher and registry inspection at source `25ce22668769dbbb8c2360f8db6e7ecc574033e1`. Interrupt is absent from the current registry. Wider matcher needs are candidates until actual tool payloads are verified.

## Notes

No Codex global configuration or active task was changed during planning. Do not broaden this task into desktop/cloud product integrations beyond preserving existing behavior.

## Deviations

None.
