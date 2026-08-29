# TASK 373: Preserve MCP cleanup failures during cancellation

## Why

MCP shutdown joins cleanup failures with `context.Canceled`, while the CLI treats any error matching cancellation as success. A real gateway close, evidence finalize, child-process, or lease-release failure can therefore be silently discarded.

## Acceptance

- Cancellation is treated as clean only when no independent cleanup failure occurred.
- Joined cleanup failures remain visible and cause a non-zero CLI result.
- Pure user cancellation retains graceful shutdown behavior.
- Tests cover each cleanup failure alone and joined with cancellation.

## Sub-Tasks

- [x] Separate expected cancellation from independent shutdown failures.
- [x] Preserve complete cleanup error context through the CLI boundary.
- [x] Add joined-error and pure-cancellation regressions.
- [x] Run focused MCP CLI and gateway shutdown tests.

## Notes

- Verified from OMP session `01a04cc5-6a01-7526-95bb-b2715553daf3`, finding #119.
- Current evidence: `internal/mcpgateway/gateway.go` joins close errors with the serve result, while `internal/cli/mcp_gateway_cmd.go` returns success for any error matching `context.Canceled`.
- TASK 291 accumulates cleanup failures inside the gateway but does not prevent the CLI from swallowing the joined error.
- The CLI now treats cancellation as graceful only when every error in the wrapped/joined tree is `context.Canceled`; gateway close, evidence finalization, child-process, and lease-release failures remain visible and non-zero when standalone or joined with cancellation.
- Focused CLI mapping and MCP gateway shutdown tests passed, including pure cancellation and each independent cleanup failure in standalone and joined forms.

## Deviations

- Per explicit execution instruction, full `make test`/race/release-trust gates and local Windows test execution were not run; retained CI and platform-specific tests were not removed or disabled.
