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

- [ ] Compare current official docs and actual CLI event loading with existing dispositions.
- [ ] Correct proven surface/configuration, normalization, and response gaps.
- [ ] Add per-mode contract regressions and duplicate/failure controls.
- [ ] Run version-bound CLI probes and update the capability/reference documentation.

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

## Deviations

None.
