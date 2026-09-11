# TASK 515: Align required-ledger interoperability with the production call budget

## Why

Windows run 34591894232 repeats the legacy form-elicitation failure at clean
commit 8810e4794167d1d88e65b19d6125c4587e28437f. The failure-only diagnostic
records 5.9096652 seconds against the raw harness's five-second call timeout.
The required ledger has verified request-accepted and pre-decision records,
with about 2.3 seconds between them, before returning `ledger_unavailable`.
This functional approval test requires eleven durable lifecycle records;
the product's normal call budget is sixty seconds.

## Acceptance

- Establish the exact fixture timeout and context propagation through the
  failing required-ledger approval path; distinguish timeout evidence from
  unrelated storage or protocol defects.
- Use the production call budget for the complete required-ledger lifecycle
  when the test has no five-second performance requirement, with a documented
  rationale and no change to product deadlines, durability, or error handling.
- Preserve signed receipt verification, exactly one downstream invocation,
  all eleven ordered ledger events, integrity, and complete lifecycle checks.
- Keep explicit timeout and cancellation regressions effective. Local gates
  and the complete native Windows suite must pass for the corrected source.

## Sub-Tasks

- [x] Inspect the raw harness, call context, approval, and required-ledger failure paths against the retained Windows diagnostic.
- [x] Correct only the functional fixture's call budget and retain exact lifecycle and failure assertions.
- [~] Run focused and required local gates, then verify the full native Windows gate before archiving, committing, and pushing.

## Notes

- The focused interoperability, deadline, timeout, and cancellation tests pass
  ten consecutive race-enabled runs (62.649 seconds). Root/template vet and
  staticcheck pass. The helper retains five seconds unless explicitly configured;
  only the required-ledger approval fixture selects `DefaultCallTimeout`.
- Complete `go test -p=2 ./...` and `make build` pass. The preceding TASK 514
  complete root/template race and release-trust gate is green; this candidate
  changes only the two test fixtures and TASK tracking.
- `Gateway.callContext` derives the configured call deadline and passes it
  through approval issuance and required-ledger writes. Both approval ledger
  failures map to `ledger_unavailable`. The retained diagnostic proves elapsed
  time beyond the fixture deadline, but does not expose the storage error;
  it does not prove an independent storage defect.
- TASK 514 candidate bcceb9d085ead6e74b5e0b2be232801b914ff241 is committed
  and pushed; its native benchmark run 34593702927 continues independently.
- Source: https://github.com/Christopher-Schulze/reconc/actions/runs/34591894232.
- Relevant boundaries: `internal/mcpgateway/protocol_e2e_test.go`,
  `internal/mcpgateway/interoperability_e2e_test.go`, `call.go`, `ledger.go`,
  `approval.go`, and the existing `DefaultCallTimeout` in `types.go`.
- A prior clean native run passed. That does not establish reliability; the
  new recurrence supplies the timing and retained-ledger evidence absent from
  the original failure. Do not rerun unchanged code merely to obtain green.

## Deviations

- Native Windows proof requires a locally verified candidate commit on main
  under the standing push instruction. Keep the TASK active until that proof
  completes; no product version, tag, or release is authorized.
