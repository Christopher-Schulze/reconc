# TASK 437: Preserve completion drift error identity

## Why

After the final retryable completion-state drift, `evaluateWithRetries` replaces the typed cause with a plain formatted error. Callers can no longer distinguish retry exhaustion with `errors.As`.

## Acceptance

- Retry exhaustion wraps a typed drift or typed exhaustion cause while preserving the current actionable message.
- Non-retryable errors retain their exact identity and are never retried.
- Public callers can distinguish transient drift exhaustion without string matching.
- Tests cover first-attempt success, recovered drift, exhausted drift, wrapped drift, and unrelated failures.

## Sub-Tasks

- [x] Define the minimal typed exhaustion contract.
- [x] Preserve the last drift cause through retry termination.
- [x] Update callers/tests that rely on the public error contract.
- [x] Run focused completion-gate tests.

## Notes

- Verified from finding 161 in `internal/completiongate/gate.go`.
- The finding was current: final retry exhaustion returned a fresh formatted error with no unwrap chain.
- `RetryExhaustedError` preserves the exact existing diagnostic, exposes the completed-attempt count, and unwraps the final typed or wrapped drift cause.
- Non-retryable errors still return after one attempt with their original identity.
- Focused tests passed: `go test ./internal/completiongate -count=1`; dependent proofbundle, TUI, and CLI packages compiled with zero selected tests.

## Deviations
