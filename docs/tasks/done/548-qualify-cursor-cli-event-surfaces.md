# TASK 548: Qualify Cursor CLI event surfaces

## Why

The product must accurately support Cursor CLI independently of desktop Cursor. Existing event dispositions contain CLI-specific restrictions that need fresh qualification, and the current live verifier can mistake Grok's agent executable for Cursor.

## Acceptance

- Cursor interactive CLI and print/headless mode each have a current event inventory with supported, observed, enforced, and unsupported states.
- Shell, file, MCP, failure, and lifecycle hooks use actual CLI contracts; desktop or cloud observations cannot qualify CLI.
- Generic and specialized events never double-count the same operation or convert passive shell notifications into successful command evidence.
- Known uncertain AskQuestion and subagent-denial behavior has a fresh recorded result or an explicit unproven limitation.
- Generated configuration preserves unrelated user hooks and survives duplicate discovery/config layers.

## Sub-Tasks

- [x] Compare current official docs and actual CLI event loading with existing dispositions.
- [x] Correct proven surface/configuration, normalization, and response gaps.
- [x] Add per-mode contract regressions and duplicate/failure controls.
- [x] Run version-bound CLI probes and update the capability/reference documentation.

## Technical Plan

1. Work through `internal/hooks/cursor_contract.go`, Cursor entries in `platforms.go`, generator/install/status owners, `internal/runtime/agentsession/cursor.go`, existing Cursor tests, and `internal/hooks/testdata/host-events/cursor.json`.
2. Use TASK 545's verified `cursor-agent` selection. Record actual help/version and supported unattended flags before writing a runner. Run interactive and print modes independently in temporary repositories. Current headless documentation requires explicit write enablement for print-mode mutation; include that in the isolated positive control, otherwise an unchanged file can falsely resemble hook enforcement. Capture structured tool-call started/completed records where the verified CLI exposes them.
3. Reconcile all 21 currently listed events with current upstream inventory. The existing `cursorDocumentedSurfaces` does not qualify shell-specific events for CLI; verify those routes before changing capability claims. The official page documents workspaceOpen for desktop and CLI, but that event proves only workspace/config liveness.
4. Use generic pre/post where available for typed enforcement/evidence, and specialized hooks only according to their actual payload. Preserve the current passive classification of afterShellExecution unless a verified contract supplies authoritative outcome data. Bind duplicate generic/specialized events to one native call or report ambiguity without double-counting.
5. Test actual return-shape semantics for block/allow, malformed JSON, hook process failure, timeout, stop continuation, and subagent behavior. Recheck current local-doc limitations for AskQuestion and SubagentStart rather than treating old reports as fixed or permanently broken.
6. Confirm project/user hook merge and third-party compatibility loading without overwriting existing configuration. Preserve existing desktop, Tab, and cloud definitions while keeping new qualification focused on CLI.
7. Propagate status surface_events, generated references, the platform fixture, `docs/documentation.md`, and the skill's platform reference.

## Verification

Run `go test ./internal/hooks ./internal/runtime/agentsession ./internal/cli`. Cover allowed/denied shell and edits, nonzero shell, MCP denial, postToolUseFailure, duplicate events, workspace without session, stop-loop bounds, canceled print runs, and the wrong agent alias. Exercise AskQuestion/subagent cases only where that mode exposes them; unavailable is not pass.

## Dependencies

TASK 545; final cross-host verification in TASK 555.

## Sources and Evidence

- [Cursor hooks](https://cursor.com/docs/hooks), [CLI usage](https://cursor.com/docs/cli/using), and [headless CLI](https://cursor.com/docs/cli/headless), checked 2026-09-12; recheck exact runner flags before implementation.
- Installed `cursor-agent` reports `2026.09.10-fd3934a`; PATH's `agent` belongs to Grok.
- The general official hooks page is not sufficient proof that every listed event is implemented in each CLI mode. That uncertainty is part of this task's acceptance evidence.

## Notes

No desktop redesign, cloud integration expansion, or changes to the user's live Cursor configuration are included.

Execution-entry check: TASK 547 was committed and pushed as `36b659905fade5a6234ec0f94c165c1e46924aa6`, and the working tree was clean before this task was activated. Local complete race, fast, vet, lint, publication, pack, and isolated self-host gates passed. GitHub checks for that new commit were only expected at push time and are not yet claimed green. Installed `cursor-agent --version` reports `2026.09.10-fd3934a`; the CLI and hook references were refreshed on 2026-09-13 before implementation.

GitHub CI and CodeQL for TASK 547's exact pushed SHA subsequently completed successfully; checked with `gh run list --commit` on 2026-09-13. This does not qualify TASK 548's uncommitted changes.

Disposable-repository CLI captures on this version: successful `--print --force --output-format stream-json` delivered workspaceOpen, sessionStart, generic pre/post/failure, beforeReadFile, before/afterShellExecution, afterFileEdit, and sessionEnd. Interactive mode delivered beforeSubmitPrompt, stop, session lifecycle, and generic/specialized read hooks. Print mode did not emit prompt or stop in the successful captured run; absence is not proof of unsupported behavior. Subsequent print probes ended in Cursor's native `WritableIterable is closed` transport error despite successful shell tool calls, so the whole runs are not positive completion controls. A failed `exit 7` produced postToolUseFailure, while afterShellExecution had no exit status. A successful shell's postToolUse carried string `tool_output`, not a structured exit status. One Write produced afterFileEdit before postToolUse; the specialized event had no tool_use_id, so the current signature including the generic ID does not deduplicate it. A failed Read and subsequent Write even reused the same tool_use_id, so the native ID alone is not a unique operation key. Captures store only field names, value types, and hashes of identifiers, not raw prompt/output content.

A second completed interactive session confirmed beforeSubmitPrompt, stop, specialized shell/file hooks, generic success/failure, and lifecycle in one run. A further interactive write probe produced two separate writes to the same file: each afterFileEdit preceded its generic postToolUse, both carried the same generation and canonical absolute path, and the specialized event still lacked a tool ID. This validates generation-and-path pairing for the observed CLI contract; it cannot prove arbitrary concurrent same-path edits in other host surfaces.

The current official postToolUse contract says `tool_output` is a JSON-stringified result. A separate CLI shell probe observed `{"exitCode":0,"output":...}` inside that string for successful commands. The normalizer previously ignored this field, and the generic post handler counted the event as shell success without inspecting the exit. This task now extracts the numeric exit and routes Cursor shell post events through strict outcome classification; missing, malformed, or nonzero exits do not create positive command evidence. The probe's overall session ended in Cursor's native transport error after three successful shell calls, so it proves the individual hook payloads, not successful session completion.

An isolated print-mode negative probe returned `permission:"deny"` from preToolUse for an exact Write target with `--force` enabled. Cursor emitted postToolUseFailure, completed its agent response, and the target file remained absent; no claim is made about other tools or host versions. Existing installer merge tests were extended to preserve a user hook and unrelated setting across two installs. At this checkpoint MCP delivery had not yet been tested; the later local-server probe below supersedes that limitation. AskQuestion and native subagent denial remain unproven rather than being inferred from desktop/forum reports.

Follow-up MCP qualification supersedes the earlier unproven-MCP note: a minimal stdio MCP server was configured only in the disposable repository. `cursor-agent --trust --approve-mcps --print --force` completed two calls to its `reconc_probe_echo` tool. Each delivered generic pre/post plus beforeMCPExecution and afterMCPExecution; the dedicated pre hook had string `tool_input`, exact server/tool names, and a stdio command locator, while the dedicated post hook carried `result_json` with `content` and `isError:false`. A separate exact MCP deny response prevented the server marker from being written on eight retries and produced generic postToolUseFailure without afterMCPExecution; that overall run ended in Cursor's transport failure, so it proves per-call native denial rather than a successful final session. The command-scoped MCP approval unexpectedly persisted for this disposable project. Its one-entry approval file was moved byte-identically into the disposable repository, and `cursor-agent mcp list` again reports the test server as needing approval; unrelated user MCP state was unchanged.

The live afterMCPExecution shape omits both the server locator and native call ID. A source review found that a locatorless post could match an unpinned MCP tool policy and create false positive write evidence. The adapter now treats this post as an unbound observation before policy classification; a real-state regression proves it cannot write evidence even when an unpinned `write_repo` policy exists. The corresponding pre event retains its exact server fingerprint and blocking decision. Safe pre/post correlation for positive MCP evidence remains unavailable without a host call identity or locator on post; do not infer it from tool/server names alone.

Final local verification on 2026-09-13: the complete uncached `make test` gate passed (including root and portable-template race suites, publication audit, harness pack, and release trust); `make vet`, `make lint`, `make self-host`, and `make reference-docs-check` passed. Live Reconc policy enforcement for Cursor CLI MCP, AskQuestion, and subagent denial was not demonstrated; the documented state remains unproven. The local release-trust gate used a synthetic temporary version and did not publish or tag this product.

## Deviations

None.
