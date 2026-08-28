# TASK 310: Derive proof-bundle executable identity from shell syntax

## Why

Proof-bundle command summaries and grouping hashes use the first `strings.Fields` token. Wrappers, assignments, `cd`, and compound shell syntax are therefore labeled as `env`, `sudo`, `cd`, or a generic environment-prefixed command instead of the effective executable.

## Acceptance

- Command identity and summary use the bounded shell parser's proven effective executable semantics.
- Environment assignments and supported wrappers are privacy-safe and do not expose argument values.
- Dynamic, compound, ambiguous, or unparsable commands receive deterministic non-colliding uncertainty identities rather than fabricated executables.
- Proof generation, verification, redaction, shell fuzz, and compatibility tests pass.

## Sub-Tasks

- [ ] Define the privacy-safe executable identity contract
- [ ] Reuse shellcommand parsing without direct-execution overclaim
- [ ] Add wrapper, assignment, compound, and malformed tables
- [ ] Run proofbundle, commandproof, shell, and fuzz gates

## Notes

- Evidence: `internal/proofbundle/bundle.go:279-347`; public docs describe this field as an executable summary.

## Deviations

None.
