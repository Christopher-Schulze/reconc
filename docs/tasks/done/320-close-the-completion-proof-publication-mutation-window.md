# TASK 320: Close the completion-proof publication mutation window

## Why

Completion captures stable state, finalizes the report, and then publishes a policy proof without a final state capture or a publication boundary tied to that snapshot. A concurrent mutation can leave a freshly written proof stale immediately.

## Acceptance

- Proof publication is conditional on the exact candidate state accepted by the completion report.
- A mutation before or during publication prevents a success proof from becoming current evidence.
- Block-decision persistence retains its documented behavior without turning stale success into allow evidence.
- Mutation-at-every-boundary and concurrent completion tests pass.

## Sub-Tasks

- [x] Map capture, finalize, and proof-store boundaries
- [x] Bind store or post-store validation to the accepted fingerprint
- [x] Remove or quarantine stale artifacts safely
- [x] Run completion, policyproof, race, and Stop gates

## Notes

- Evidence: `internal/completiongate/gate.go:297-327`. Later proof loading revalidates, so the current issue is stale publication and false freshness, not a proven authorization bypass.
- Policy-decision publication now captures the candidate immediately before and after `policyproof.Store`; any fingerprint drift returns the typed retryable error before the report is accepted.
- Blocking receipts remain full-fingerprint bound when post-publication drift occurs; non-blocking persistence still clears older blocks only through the same stable boundary, so stale success never becomes positive evidence.
- Deterministic boundary tests cover mutation before publication, mutation after publication, stable block retention, and stale-success clearing. Completion, policyproof, proof-bundle, CLI, race, `make test-fast`, `make vet`, Staticcheck v0.8.1, and `make self-host` gates passed.

## Deviations

None.
