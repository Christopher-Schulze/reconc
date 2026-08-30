# TASK 446: Isolate invalid upstream MCP requests

## Why

The strict transforming frame reader permanently latches every transformer error. One request-local issue such as a duplicate active ID or reserved correlation key then terminates all later traffic on the MCP connection.

## Acceptance

- Request-local validation failures produce a bounded JSON-RPC error for that request and do not forward it downstream.
- Framing, transport, and unrecoverable correlation-state failures still terminate the connection fail closed.
- Observer maps remain consistent after every rejected request and response.
- Protocol tests cover duplicate IDs, reserved metadata, malformed params, oversize transforms, later valid calls, and true stream corruption.

## Sub-Tasks

- [x] Classify transformer failures as request-local or connection-fatal.
- [x] Add a bounded response path that preserves stream ordering and correlation state.
- [x] Add multi-request protocol regressions.
- [x] Run focused MCP frame and upstream protocol tests.

## Notes

- Verified from finding 183 in `internal/mcpgateway/frame.go` and `upstream.go`.
- Request-local transform errors carry only a standard code and static bounded message. The strict reader writes them through the serialized upstream writer without invoking response correlation cleanup, clears the rejected frame, and continues at the next frame.
- Duplicate IDs preserve the original active request; rejected correlation injection removes only its provisional state. Framing/parser failures and correlation-sequence exhaustion still latch the reader error and terminate the stream.

## Deviations
