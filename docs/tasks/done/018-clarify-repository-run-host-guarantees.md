# TASK 018: Clarify repository-run host guarantees

## Why

The implementation distinguishes repository scope, typed TASK exhaustion, six synchronous native Stop gates, and two inferred best-effort idle adapters. Short-form documentation must not collapse those different guarantees into an ambiguous claim that every host runs identically or that an empty `Current:` always ends execution.

## Acceptance

- Every short-form run-control guide states that the switch is repository-scoped rather than machine-global.
- Native synchronous Stop coverage and inferred OpenCode/Kilo continuation are distinguished consistently.
- TASK exhaustion, queue claim, blocker, invalid-state, interrupt, and no-progress behavior match the implementation.
- Focused behavior tests, full tests, documentation consistency checks, and the clean-repository bootstrap golden path pass.
- The completed change is archived and committed as one TASK.

## Sub-Tasks

- [x] Align public, agent, embedded, and scaffolded run-control guidance.
- [x] Verify code-to-documentation parity and tests.
- [x] Archive and commit the TASK.

## Notes

- No runtime behavior changes are required; native-shape tests already exercise continuation through all eight agent adapters.
- Golem remained read-only. Changes since the earlier import touch only Omnimus-specific naming, egress, and process-hardening audit exemptions; they do not add a reusable Reconc product mechanism.
- Focused run-control, CLI, TASK lifecycle, hook, and embedded-guide tests passed uncached. Full product tests, template-harness tests, vet, build, release trust, and the clean-repository bootstrap golden path passed.
- The first stale-name grep intentionally matched the removal documentation and failable compatibility tests. The corrected production-surface check found no active `runloop`, `/runloop`, or `degenmode` command path and preserved those negative proofs.
- Final reality check found one durable repository switch, eight registered agent adapters, six synchronous native Stop gates, two inferred idle adapters, no active legacy loop surface, and no new reusable Golem mechanism left to port.

## Deviations

None.
