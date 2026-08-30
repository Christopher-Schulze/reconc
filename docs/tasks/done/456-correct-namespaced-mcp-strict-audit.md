# TASK 456: Correct namespaced MCP strict audit

## Why

Namespaced MCP post events set `BlockingPreHook=false` because the current event is post-result. Audit interprets that field as platform strict availability, so each successful post event increments `StrictUnavailable` even though its paired pre route is blocking.

## Acceptance

- Audit strict availability represents the platform/route enforcement capability, not the phase of the current event.
- Paired pre/post events aggregate under one stable capability identity without double-counting failures.
- Native MCP platforms and genuinely non-blocking routes retain correct statistics.
- Tests cover Claude/Codex namespaced pre/post pairs, post-only drift, native routes, denial, failure, and unclassified events.

## Sub-Tasks

- [x] Separate event phase from strict-capability evidence in the normalized envelope/audit API.
- [x] Derive namespaced post capability from the declared paired pre route.
- [x] Add audit aggregation regressions.
- [x] Run focused MCP adapter and audit tests.

## Notes

- Verified from finding 205 in `namespaced_mcp.go`, `mcp.go`, and `mcp_audit.go`.
- Added an explicit `before`/`after` phase while retaining `BlockingPreHook` as
  route capability; the audit API now derives strict availability only from the
  normalized envelope.
- Claude/Codex pairs, post-only drift, denial, classified failure,
  unclassified calls, native Pi routes, and a genuinely non-blocking route are
  covered by focused regressions.
- Focused MCP adapter/audit tests passed with `-count=1`.

## Deviations
