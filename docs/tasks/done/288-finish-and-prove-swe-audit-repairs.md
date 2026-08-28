# TASK 288: Finish and prove the SWE audit repairs

## Why

The SWE-1.7 audit modified five production files and added one JSONL test despite a read-only instruction. Two edits repair real defects, while three are defensive error-propagation changes. The uncommitted repair set needs exact regression coverage and one task-owned review before it can be accepted.

## Acceptance

- The reservation error overwrite and reservation-release paths in `internal/mcpgateway/call.go` have direct fault-injection regressions.
- The JSONL caller-buffer mutation regression remains failable against the old implementation.
- Impossible `action.Value` accessor failures fail closed rather than silently continuing with partial MCP content.
- Every changed API and caller is re-read; focused race tests, `make test`, Vet, Staticcheck, and diff checks pass.
- No SWE change is restored unless a regression proves it wrong; unrelated dirty work is preserved.

## Sub-Tasks

- [x] Add exact MCP reservation and ledger-failure regressions
- [x] Review the MCP result accessor branches for fail-closed behavior
- [x] Revalidate the JSONL mutation test against the pre-fix behavior
- [x] Run focused race and complete repository gates
- [x] Accept or surgically correct the repair set

## Notes

- `prepareCall` now returns the original reservation failure before ledger construction and releases a successful reservation when ledger construction or `requestAccepted` fails. The fault-injection regression reaches all three paths and proves no dispatch plus zero live reservations.
- JSONL normalization now copies into one exact-size owned buffer before append. The table regression preserves the complete caller backing array for plain, newline, CRLF, and empty records; the plain and CRLF cases fail against the pre-fix append behavior.
- MCP result traversal now converts every previously ignored accessor failure in `mcp_result.go` into a typed malformed-result error instead of continuing with partial content.
- Inspection-evidence and completion-report serialization errors propagate to callers. Existing completion tests now check `finalize` errors, and an unmarshalable-payload regression covers the hash failure.
- Focused package tests and focused race tests pass. The final tree passed `make test` twice, plus `make vet`, `make lint`, and `git diff --check`.
- No user-facing contract changed, so README, command, architecture, and documentation SSOT text remain accurate without a product-documentation edit.

## Deviations

None.
