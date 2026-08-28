# TASK 310: Derive proof-bundle executable identity from shell syntax

## Why

Proof-bundle command summaries and grouping hashes use the first `strings.Fields` token. Wrappers, assignments, `cd`, and compound shell syntax are therefore labeled as `env`, `sudo`, `cd`, or a generic environment-prefixed command instead of the effective executable.

## Acceptance

- Command identity and summary use the bounded shell parser's proven effective executable semantics.
- Environment assignments and supported wrappers are privacy-safe and do not expose argument values.
- Dynamic, compound, ambiguous, or unparsable commands receive deterministic non-colliding uncertainty identities rather than fabricated executables.
- Proof generation, verification, redaction, shell fuzz, and compatibility tests pass.

## Sub-Tasks

- [x] Define the privacy-safe executable identity contract
- [x] Reuse shellcommand parsing without direct-execution overclaim
- [x] Add wrapper, assignment, compound, and malformed tables
- [x] Run proofbundle, commandproof, shell, and fuzz gates

## Notes

- Evidence: `internal/proofbundle/bundle.go:279-347`; public docs describe this field as an executable summary.
- Static single-invocation hashes retain the format-1 executable-name identity; uncertainty classes use a NUL-domain prefix so they cannot collide with a real executable identity.
- Shell AST syntax, not flattened word values, owns assignment, redirect, and control positions; quoted lookalikes remain real executable words.
- Summaries and identities exclude arguments and assignment values, round-trip through proof verification, and classify unrepresentable executable names as ambiguous rather than truncating or guessing.
- Focused package tests, three fuzz targets, `make test`, `make vet`, Staticcheck v0.8.1, and `make self-host` passed.

## Deviations

None.
