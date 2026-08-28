# TASK 320: Close the completion-proof publication mutation window

## Why

Completion captures stable state, finalizes the report, and then publishes a policy proof without a final state capture or a publication boundary tied to that snapshot. A concurrent mutation can leave a freshly written proof stale immediately.

## Acceptance

- Proof publication is conditional on the exact candidate state accepted by the completion report.
- A mutation before or during publication prevents a success proof from becoming current evidence.
- Block-decision persistence retains its documented behavior without turning stale success into allow evidence.
- Mutation-at-every-boundary and concurrent completion tests pass.

## Sub-Tasks

- [ ] Map capture, finalize, and proof-store boundaries
- [ ] Bind store or post-store validation to the accepted fingerprint
- [ ] Remove or quarantine stale artifacts safely
- [ ] Run completion, policyproof, race, and Stop gates

## Notes

- Evidence: `internal/completiongate/gate.go:217-253`. Later proof loading revalidates, so the current issue is stale publication and false freshness, not a proven authorization bypass.

## Deviations

None.
