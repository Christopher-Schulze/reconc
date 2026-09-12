# TASK 545: Bind live hook proof to host execution

## Why

Current live verification is operator-driven, lacks executable/version binding, and can infer enforcement from one blocked record plus two absent markers. That does not prove that both operations were attempted and denied by the actual host.

## Acceptance

- Every live result identifies the resolved host executable, reported version, binary digest, surface/mode, Reconc build and adapter digest, probe/run identity, repository/config identity, and time.
- Each enforcement claim has its own attempted operation, decision, observed host outcome, and independent side-effect check. Missing events, unsupported routes, authentication failures, timeouts, and skips cannot become pass.
- A wrong executable named `agent` is rejected as Cursor. All five requested hosts have explicit discovery and version-reporting contracts.
- Synthetic adapter tests remain visibly distinct from real host qualification. Version or configuration changes invalidate reuse of the old qualification.
- The existing CLI and probe script expose bounded, machine-readable verification without changing ordinary user sessions or the product repository.

## Sub-Tasks

- [ ] Define the typed receipt and per-operation evidence model against the current verifier and schemas.
- [ ] Implement exact executable discovery, version qualification, and causal probe accounting.
- [ ] Add bounded host runners or explicit operator-assisted steps where no supported unattended interface exists.
- [ ] Exercise positive and negative controls and propagate status, diagnostics, schema, and documentation.

## Technical Plan

1. Extend `internal/cli/hook_verify_cmd.go`, `hook_verify_live.go`, `internal/hooks/verification.go`, and their existing tests. Reuse `scripts/tests/host-integration-probe.sh`; do not build a second verification framework.
2. Replace availability-only PATH checks with host-specific identification. Prefer `cursor-agent`; accept `agent` only after positive Cursor identity verification. Add Codex and Devin, which currently reach the unchecked default, and DSH once TASK 550 provides the platform. Resolve symlinks and record the executable actually launched.
3. Track read, allowed write, denied write, allowed shell, denied shell, nonzero shell, and classified MCP operations independently. Give each operation a fresh nonce and distinct marker. A successful allowed operation is the positive control that the host and probe actually ran. A missing marker alone proves nothing.
4. Require matching pre-decision and host-attempt/result evidence for each denied operation. Where the host omits a result after denial, use its explicit rejection plus the linked attempted call. Never equate operator Enter, route liveness, or a synthetic invocation with native enforcement.
5. Keep current isolated-workspace creation and output limits. Add an overall deadline, per-action deadlines, bounded retries only for diagnosed transient setup errors, cancellation of child processes, and redacted bounded logs. Do not change authentication configuration or attach to a user's active conversation.
6. Use documented headless, SDK, or interactive interfaces only after reading their exact current flags. Preserve the explicit `--allow-authenticated` contract. JSON/noninteractive invocation must never wait indefinitely for Enter. Operator-assisted results must identify that mode.
7. Extend schemas through their current registry owner and regenerate affected reference docs. Reuse existing verification/status fields where compatible; introduce a format revision only if required by the schema's compatibility policy, without changing immutable historical schema identities.

## Verification

Run `go test ./internal/cli ./internal/hooks ./internal/schema` with focused regressions first. Include the wrong-host alias, zero attempted operations, only one of two deny probes, stale receipts, mismatched repository, changed config, duplicate/replayed IDs, canceled host, malformed output, and missing authentication. Real-host qualification is owned by TASK 555 after the adapters are complete.

## Dependencies

TASK 544 for reliable closure gates. TASKS 546-550 supply host-specific runners and fixtures; TASK 545 defines their shared contract first.

## Notes

At the planning baseline, PATH resolves `agent` to `~/.grok/bin/agent`; the actual Cursor executable is `~/.local/bin/cursor-agent`. Current `applyLiveHookProbeRecords` derives enforcement from `summary.blocked` and both markers being absent. Local installed versions: Codex `0.154.0`, Devin `3000.10.21 (611c1cba)`, Cursor `2026.09.10-fd3934a`, OMP `18.1.18`. DSH was not found on PATH. These observations are inventory, not live enforcement evidence.

Execution-entry review also confirms that `initializeHookVerificationRepo` currently creates only a deny_write rule for forbidden.txt, not a policy against the separate forbidden-command-marker. The capture shim derives decisions from wrapper exit status alone and does not parse event-specific JSON denials. The existing `TestApplyLiveHookProbeRecordsSeparatesObservedFromComplete` expects Enforced after one generic blocked record; update that expectation with the causal-proof implementation because it currently encodes the incorrect inference. Actual CLI help was inspected without launching agent runs: Codex offers exec JSONL/ephemeral/ignore-user-config and explicit hook-trust control; Devin offers print, explicit config, permission mode, and workspace trust; Cursor offers print/stream-json/workspace/trust; OMP offers print/json/cwd/no-session/config. Qualify exact combinations and keep approved credentials separate from disposable session/config state.

## Deviations

None. No authenticated host probes were launched during planning.
