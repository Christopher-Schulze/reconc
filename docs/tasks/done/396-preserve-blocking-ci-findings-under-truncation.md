# TASK 396: Preserve blocking CI findings under truncation

## Why

`cireport.FromCheck` truncates the source-order violation slice before sorting or prioritizing severity. A blocking decision with late error-level violations can therefore produce SARIF or JUnit output containing only non-error findings.

## Acceptance

- Truncation preferentially retains error-level findings while preserving deterministic ordering.
- A blocking report cannot become pass-shaped solely because its findings exceeded the cap.
- Every truncated artifact carries an explicit deterministic truncation finding or equivalent machine-readable notice.
- Tests cover mixed severity beyond the cap for JSON, SARIF, and JUnit renderers.

## Sub-Tasks

- [x] Reuse or generalize the existing error-preserving bounded insertion logic.
- [x] Verify the stable truncation signal in all CI artifact formats.
- [x] Add adversarial late-blocker regressions.
- [x] Run focused CI report tests.

## Notes

- Verified from finding 54.
- `FromImpact` already replaces a retained non-error when a later error arrives; `FromCheck` currently slices first and sorts afterward.
- The adversarial regression reproduced the defect before the fix: 1,024 warnings followed by one blocking violation produced a block decision with zero retained error findings.
- `FromCheck` now uses the same bounded error-preserving insertion contract as `FromImpact`; late non-blocking violations increment the exact omission count without paying sanitization costs.
- SARIF retains `truncated_findings`, JUnit retains `reconc.truncated_findings`, and GitHub output retains its deterministic omission notice.
- The complete `internal/cireport` package and `make test-fast` passed.

## Deviations
