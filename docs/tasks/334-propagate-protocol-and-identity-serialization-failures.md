# TASK 334: Propagate protocol and identity serialization failures

## Why

Several identity and protocol helpers discard `json.Marshal` errors and continue with empty bytes, an empty digest, a newline-only frame, or empty hook output. Current concrete values are usually marshalable, but the boundary silently fails if a type evolves.

## Acceptance

- Every production serialization used for identity, digest, protocol response, or hook control output returns and propagates its error.
- No empty-body hash, empty digest, newline-only protocol frame, or invalid host response can be emitted after encoding failure.
- Public API changes are minimal and every caller handles failure explicitly and fail closed.
- Fault-injected marshal tests remain failable without introducing unmarshalable production fields.

## Sub-Tasks

- [ ] Inventory ignored JSON errors at identity and protocol boundaries
- [ ] Classify impossible versus externally reachable failures
- [ ] Propagate errors through action, custom-runtime, agent-session, and proof callers
- [ ] Run protocol, identity, adapter, and full gates

## Notes

- Evidence includes `internal/action/evaluator.go` approval identity, `internal/customruntime/response.go` and `types.go`, agent-session response helpers, runtime completion hashing, and proof-bundle digest helpers. TASK 288 already owns the dirty action-inspection and completion-gate instances.

## Deviations

None.
