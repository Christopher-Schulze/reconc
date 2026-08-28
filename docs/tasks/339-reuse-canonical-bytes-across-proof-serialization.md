# TASK 339: Reuse canonical bytes across proof serialization

## Why

Policy-proof and proof-bundle creation marshal overlapping report, digest payload, verification payload, and final indented artifact trees multiple times. Large completion artifacts repay full-tree traversal and allocation at each stage.

## Acceptance

- Each distinct canonical payload is encoded once and reused only where byte contracts are identical.
- Self-digests, report hashes, indented publication, verification, schema, and backward compatibility remain exact.
- Error propagation replaces empty-digest fallbacks.
- Maximum-artifact allocation benchmarks and tamper tests pass.

## Sub-Tasks

- [ ] Diagram proof payload and byte-identity boundaries
- [ ] Cache canonical bytes within one generate or verify operation
- [ ] Keep presentation bytes separate where indentation changes identity
- [ ] Run policyproof, proofbundle, completion, schema, and benchmark gates

## Notes

- Evidence: `internal/policyproof/proof.go` record construction/validation and `internal/proofbundle/bundle.go` generation, digest, verification, and marshaling.

## Deviations

None.
