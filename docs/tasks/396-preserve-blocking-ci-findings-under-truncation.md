# TASK 396: Preserve blocking CI findings under truncation

## Why

`cireport.FromCheck` truncates the source-order violation slice before sorting or prioritizing severity. A blocking decision with late error-level violations can therefore produce SARIF or JUnit output containing only non-error findings.

## Acceptance

- Truncation preferentially retains error-level findings while preserving deterministic ordering.
- A blocking report cannot become pass-shaped solely because its findings exceeded the cap.
- Every truncated artifact carries an explicit deterministic truncation finding or equivalent machine-readable notice.
- Tests cover mixed severity beyond the cap for JSON, SARIF, and JUnit renderers.

## Sub-Tasks

- [ ] Reuse or generalize the existing error-preserving bounded insertion logic.
- [ ] Add a stable truncation signal to all CI artifact formats.
- [ ] Add adversarial late-blocker regressions.
- [ ] Run focused CI report tests.

## Notes

- Verified from finding 54.
- `FromImpact` already replaces a retained non-error when a later error arrives; `FromCheck` currently slices first and sorts afterward.

## Deviations
