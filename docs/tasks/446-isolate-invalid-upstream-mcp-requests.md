# TASK 446: Isolate invalid upstream MCP requests

## Why

The strict transforming frame reader permanently latches every transformer error. One request-local issue such as a duplicate active ID or reserved correlation key then terminates all later traffic on the MCP connection.

## Acceptance

- Request-local validation failures produce a bounded JSON-RPC error for that request and do not forward it downstream.
- Framing, transport, and unrecoverable correlation-state failures still terminate the connection fail closed.
- Observer maps remain consistent after every rejected request and response.
- Protocol tests cover duplicate IDs, reserved metadata, malformed params, oversize transforms, later valid calls, and true stream corruption.

## Sub-Tasks

- [ ] Classify transformer failures as request-local or connection-fatal.
- [ ] Add a bounded response path that preserves stream ordering and correlation state.
- [ ] Add multi-request protocol regressions.
- [ ] Run focused MCP frame and interoperability tests.

## Notes

- Verified from finding 183 in `internal/mcpgateway/frame.go` and `upstream.go`.

## Deviations
