# TASK 349: Preserve distinct tool-use event identities

## Why

Material-event deduplication hashes tool name, input, and outcome but omits the tool-use identifier. Consecutive distinct invocations with identical content can collapse into one event and undercount runtime evidence.

## Acceptance

- Material-event identity distinguishes different non-empty tool-use IDs.
- Retries or duplicate delivery of the same tool-use event remain deduplicated.
- Legacy events without tool-use IDs retain deterministic behavior.
- Tests cover identical payloads with same, different, and absent identifiers.

## Sub-Tasks

- [x] Define backward-compatible tool-use identity semantics.
- [x] Include the identifier in material-event signatures where present.
- [x] Add deduplication and evidence-count regressions.
- [x] Run focused runtime event tests.

## Notes

- Verified from OMP session `01a04cc5-6a01-7526-95bb-b2715553daf3`, finding #94.
- Reverified the omission in `materialEventSignature`; command-result parsing already retained the identifier.
- Material signatures now include a trimmed non-empty `tool_use_id` with `omitempty`, preserving legacy no-ID hashes while separating distinct invocations. MCP material signatures use the same rule.
- Same-ID retries deduplicate; different IDs and legacy no-ID events follow deterministic evidence-count behavior.
- Focused `agentsession` tests passed on macOS. Windows adapter code remains covered by CI only.

## Deviations

None.
