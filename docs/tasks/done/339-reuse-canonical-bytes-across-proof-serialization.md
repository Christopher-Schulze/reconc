# TASK 339: Reuse canonical bytes across proof serialization

## Why

Policy-proof and proof-bundle creation marshal overlapping report, digest payload, verification payload, and final indented artifact trees multiple times. Large completion artifacts repay full-tree traversal and allocation at each stage.

## Acceptance

- Each distinct canonical payload is encoded once and reused only where byte contracts are identical.
- Self-digests, report hashes, indented publication, verification, schema, and backward compatibility remain exact.
- Error propagation replaces empty-digest fallbacks.
- Maximum-artifact allocation benchmarks and tamper tests pass.

## Sub-Tasks

- [x] Diagram proof payload and byte-identity boundaries
- [x] Cache canonical bytes within one generate or verify operation
- [x] Keep presentation bytes separate where indentation changes identity
- [x] Run policyproof, proofbundle, completion, schema, and benchmark gates

## Notes

- Evidence: `internal/policyproof/proof.go` record construction/validation and `internal/proofbundle/bundle.go` generation, digest, verification, and marshaling.
- Canonical policy-report bytes are encoded once per record construction or validation and embedded as `json.RawMessage` for the enclosing digest payload. Proof-bundle generation validates fields without a pre-digest round trip, hashes one compact payload, and keeps indented JSON as a separate presentation encoding.
- Verified with `go test ./internal/policyproof ./internal/proofbundle ./internal/completiongate -count=1` and `go test ./internal/proofbundle -run '^$' -bench 'Benchmark(Digest|MarshalJSON)MaximumArtifact$' -benchmem -count=1`.

## Deviations

None.
