# TASK 367: Bind substantive proof measurements to evidence

## Why

Substantive proof validates declared sample values and independently verifies a hash for an arbitrary evidence file. Nothing proves that the declared measurements were extracted from or represented by those hashed bytes.

## Acceptance

- Every substantive-proof sample is deterministically derived from or cryptographically bound to the referenced evidence bytes.
- A valid evidence hash with unrelated declared samples fails closed.
- Supported evidence formats have explicit parsing and validation rules.
- Tests cover sample tampering, unrelated evidence, malformed evidence, and valid proofs.

## Sub-Tasks

- [x] Define the canonical binding between evidence bytes and measured samples.
- [x] Implement format-aware extraction or an equivalent verifiable commitment.
- [x] Update proof validation and diagnostics.
- [x] Add adversarial proof regressions and run focused assurance tests.

## Notes

- Verified from OMP session `01a04cc5-6a01-7526-95bb-b2715553daf3`, finding #113.
- Current evidence: `internal/assurance/proof.go` validates samples separately from the evidence-file hash.
- Current documentation claims measured samples and byte-matched evidence agree, which is not presently proven.
- Evidence validation now parses one of the documented sample-bearing formats (strict JSON object/array or the established `measured samples:` / `benchmark samples:` text line), requires finite values, and compares the exact ordered float sequence against the proof before threshold evaluation.
- Duplicate evidence JSON keys and unknown fields fail closed. Regressions cover unrelated samples with a valid hash, malformed evidence, and valid JSON evidence. `go test ./internal/assurance -count=1 -timeout=120s`, `go vet ./internal/assurance`, `make fmt-check`, and `make reference-docs-check` passed.

## Deviations

- The repository-wide race, release-trust, publication, and other heavy suites were not run, per the explicit execution constraint. Windows-specific tests were not run locally; cross-platform source compatibility remains covered by CI.
