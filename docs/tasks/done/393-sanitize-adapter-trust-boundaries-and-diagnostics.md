# TASK 393: Sanitize adapter trust boundaries and diagnostics

## Why

Clone-based host adapters retain an incoming `reconc_mcp` object on non-MCP events, allowing raw host payloads to inject trusted MCP evidence. Custom runtimes set `input_valid: true` without validating mapped input, Devin omits route and working-directory binding, Copilot rejects valid repository subdirectories, and adapter failures echo untrusted tool names or output into host-visible deny reasons.

## Acceptance

- Only Reconc-created MCP envelopes reach shared payload parsing; all foreign `reconc_mcp` fields are removed before event-specific reconstruction.
- Custom-runtime `input_valid` reflects an actual mapped JSON-object check or is omitted and derived by the consumer.
- Devin accepts only its declared native event for the selected route and binds payload CWD within the repository; Copilot uses the same identity-safe containment contract.
- Host-visible reasons never contain raw payload text, control characters, secrets, or unbounded excerpts.
- Adversarial tests cover forged envelopes, route mismatch, foreign and nested CWDs, malformed custom inputs, prompt-injection strings, log forging, Unicode, and size boundaries across every affected adapter.

## Sub-Tasks

- [x] Map each adapter's raw-to-neutral trust boundary and result adaptation path.
- [x] Strip foreign envelopes and derive trusted fields from validated inputs only.
- [x] Introduce bounded safe diagnostics with private detailed errors where appropriate.
- [x] Run focused adapter, hook-runtime, and custom-runtime tests.

## Notes

- Verified from findings 48, 49, 52, 204, and 208.
- Affected clone paths include Cursor, Grok, GitHub Copilot, Devin, and Antigravity; JSON escaping prevents structural injection but not semantic prompt injection.
- Current-code proof: every named normalizer cloned the raw top-level map and retained a caller-supplied `reconc_mcp`; `NormalizeDevinPayload` accepted every route string and never inspected `cwd`; Copilot compared only exact root identity; custom MCP construction set `input_valid: true` unconditionally; normalization errors were concatenated directly into Copilot and Grok decision reasons.
- The implemented boundary removes foreign envelopes before reconstruction, binds Devin and Copilot to the shared identity-resolved containment check, derives custom MCP input validity from a mapped object, makes pre-normalization adapter failures categorical, and normalizes trusted runtime reasons to one bounded line.
- Focused adapter, custom-runtime, hook-runtime, offline-verification, and complete `internal/runtime/agentsession`, `internal/customruntime`, and `internal/cli` tests passed; `make test-fast` passed the full root and portable-template modules.

## Deviations
