# TASK 437: Preserve completion drift error identity

## Why

After the final retryable completion-state drift, `evaluateWithRetries` replaces the typed cause with a plain formatted error. Callers can no longer distinguish retry exhaustion with `errors.As`.

## Acceptance

- Retry exhaustion wraps a typed drift or typed exhaustion cause while preserving the current actionable message.
- Non-retryable errors retain their exact identity and are never retried.
- Public callers can distinguish transient drift exhaustion without string matching.
- Tests cover first-attempt success, recovered drift, exhausted drift, wrapped drift, and unrelated failures.

## Sub-Tasks

- [ ] Define the minimal typed exhaustion contract.
- [ ] Preserve the last drift cause through retry termination.
- [ ] Update callers/tests that rely on the public error contract.
- [ ] Run focused completion-gate tests.

## Notes

- Verified from finding 161 in `internal/completiongate/gate.go`.

## Deviations
