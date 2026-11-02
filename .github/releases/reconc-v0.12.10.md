# reconc v0.12.10

Reconc 0.12.10 lets an agent record an explicitly chat-approved, timed operator
claim without requiring the user to enter a terminal confirmation code.

## Timed operator claims

- `hook claim --user-approved --for DURATION` records the agent's attestation
  that the user approved the exact operator claim, scope and duration. The
  existing one-minute to eight-hour bounds, session binding, expiry and
  revocation apply across commits within that window.
- The terminal confirmation route remains the default. Missing durations,
  non-operator claims, revocation combinations and dynamic shell arguments
  are rejected by the corresponding CLI or native command guard.
- Agent attestation is not independent user authentication. Hook pause,
  resume and evidence recovery retain their separate authority boundaries.

## Hook transport tests

- DeepSeek Harness transport success tests use the production deadline while
  timeout tests retain their deliberately short budget.
