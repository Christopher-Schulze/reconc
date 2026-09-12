# TASK 547: Close Devin tool and session gaps

## Why

The generated Devin pre/permission matcher is limited to exec and edit. The current documented tool surface includes additional file mutations and MCP calls. Session normalization also falls back to a repository-derived shared identity despite current documented per-session and per-turn identifiers.

## Acceptance

- Every currently supported Devin file mutation and shell/MCP entrypoint is either classified and gated or explicitly reported unsupported.
- Two concurrent Devin sessions in one repository cannot share policy evidence through a repository-only identity fallback.
- Actual successful, failed, canceled, denied, and rewritten operations retain truthful call/turn binding.
- Native and compatibility-loaded hooks do not double-count events.
- Version-qualified tests and live results distinguish host timeout behavior from Reconc's own failure handling.

## Sub-Tasks

- [ ] Record current event/tool payloads and compare generator, normalizer, and MCP classification.
- [ ] Close matcher/normalization gaps and correct session/turn identity handling.
- [ ] Add concurrent-session, process-control, rewrite, and response-shape regressions.
- [ ] Update fixtures, docs, and live qualification for the verified Devin version.

## Technical Plan

1. Change the Devin generator in `internal/hooks/platform_generators.go`, registry entries in `platforms.go`, `internal/runtime/agentsession/devin.go`, existing Devin tests, and the namespaced MCP owner only where needed.
2. The official lifecycle list includes `write`, `apply_patch`, `notebook_edit`, process interaction, `mcp_call_tool`, and namespaced MCP tools beyond the current pre matcher. Build a typed normalization table from observed payloads. For process interaction, bind stdin and completion to the original execution; never blindly mark a polling call successful command evidence.
3. Extend pre/permission/post matchers consistently. Normalize direct file mutation arguments through existing path/write accounting. Route native MCP dispatch envelopes and namespaced tools through `mcp_payload.go` and `namespaced_mcp.go`; do not confuse MCP management or resource reads with downstream writes.
4. Preserve native `session_id` and bind `prompt_id` where present. Missing identity on a policy-relevant event must produce an explicit degraded/refused state rather than silently merging unrelated sessions. If older hosts remain supported, constrain their fallback to a separately proven session identity and document that compatibility boundary.
5. Validate event-name/CWD checks, forged envelope rejection, input rewriting order, and authoritative post-result fields. A successful hook process or textual output is not a successful tool execution.
6. Test the host's handling of exit codes, malformed output, timeouts, and duplicate Claude-compatible configuration. Retain bounded Stop follow-ups and reinject context after compaction without replaying evidence.

## Verification

Run `go test ./internal/hooks ./internal/runtime/agentsession ./internal/cli`. Required isolated scenarios: concurrent sessions, first prompt without earlier prompt_id, changed prompt_id, missing session ID, denied direct write/apply_patch/notebook edit, failed shell, process input/completion, direct/namespaced MCP, duplicate compatibility hooks, and conflicting rewritten inputs. Live verification uses the actual Devin executable with TASK 545 receipts.

## Dependencies

TASK 545. Coordinate shared MCP changes with TASKS 546 and 550 and context behavior with TASK 553.

## Sources and Evidence

- [Devin lifecycle, identity, and tool inventory](https://docs.devin.ai/cli/extensibility/hooks/lifecycle-hooks).
- [Devin hook configuration and output contract](https://docs.devin.ai/cli/extensibility/hooks/overview).
- Checked 2026-09-12; installed binary reports `3000.10.21 (611c1cba)`. Current source observations: `^(exec|edit)$` pre/permission matcher and repository-SHA fallback in `NormalizeDevinPayload`. Native identity requirements must be qualified against that exact binary, not inferred only from documentation.

## Notes

The matcher omission and shared fallback exist in source. Whether every documented newer tool is exposed by the installed model/mode remains a live qualification item.

## Deviations

None.
