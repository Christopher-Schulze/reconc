# TASK 503: Reject overflowed session reports as current briefing evidence

## Why

Session report binding currently verifies report, evidence, and candidate hashes but does not reject a session state marked with evidence overflow. A bounded report can therefore become a current briefing blocker even though the evidence set is explicitly uncertified.

## Acceptance

- A saved report is never classified as current while `SessionState.EvidenceOverflow` is true.
- The report remains available as historical evidence with a bounded, machine-readable reason.
- The behavior is covered by a regression test and preserves existing current-report behavior after overflow is cleared.
- Session-briefing documentation states the overflow boundary.

## Sub-Tasks

- [x] Add the overflow binding guard and regression coverage.
- [x] Update the session-briefing contract documentation.
- [x] Run focused tests and required repository checks, then archive this task.

## Notes

- Discovered during the read-only reality audit of TASK 477.
- Verification: `go test ./internal/runtime/agentsession -run 'TestInspectSessionReportBinding' -count=1`; `make test-fast`; `git diff --check` all passed.

## Deviations

None planned.
