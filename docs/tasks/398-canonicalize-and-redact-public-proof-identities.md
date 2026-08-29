# TASK 398: Canonicalize and redact public proof identities

## Why

Policy-proof validation accepts uppercase hexadecimal fingerprints while completion compares the same identity as a case-sensitive string. Public proof sanitization also ignores `USERNAME` and deliberately skips one- and two-character operator names.

## Acceptance

- Persisted policy-proof and assurance digest identities accept only the canonical lowercase encoding produced by Reconc, with no surrounding whitespace.
- Every consumer uses one canonical equality contract for candidate fingerprints.
- Public proof text redacts Unix and Windows operator identities on token boundaries without corrupting unrelated short text.
- Adversarial tests cover mixed-case digests, forged current decisions, short usernames, `USER`, `USERNAME`, Windows paths, and boundary collisions.

## Sub-Tasks

- [ ] Enforce lowercase digest shape at policy-proof and assurance decode/write boundaries.
- [ ] Audit all candidate-fingerprint comparisons for canonical equality.
- [ ] Extend operator redaction with cross-platform, boundary-aware handling.
- [ ] Run focused policy-proof, completion-gate, and proof-bundle tests.

## Notes

- Verified from findings 57, 58, and 170.
- Finding 59 was rejected: the exported TASK identity is the validated numeric task ID, not the free-form logbook slug claimed by the finding.

## Deviations
