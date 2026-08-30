# TASK 398: Canonicalize and redact public proof identities

## Why

Policy-proof validation accepts uppercase hexadecimal fingerprints while completion compares the same identity as a case-sensitive string. Public proof sanitization also ignores `USERNAME` and deliberately skips one- and two-character operator names.

## Acceptance

- Persisted policy-proof and assurance digest identities accept only the canonical lowercase encoding produced by Reconc, with no surrounding whitespace.
- Every consumer uses one canonical equality contract for candidate fingerprints.
- Public proof text redacts Unix and Windows operator identities on token boundaries without corrupting unrelated short text.
- Adversarial tests cover mixed-case digests, forged current decisions, short usernames, `USER`, `USERNAME`, Windows paths, and boundary collisions.

## Sub-Tasks

- [x] Enforce lowercase digest shape at policy-proof and assurance decode/write boundaries.
- [x] Audit all candidate-fingerprint comparisons for canonical equality.
- [x] Extend operator redaction with cross-platform, boundary-aware handling.
- [x] Run focused policy-proof, completion-gate, and proof-bundle tests.

## Notes

- Verified from findings 57, 58, and 170.
- Finding 59 was rejected: the exported TASK identity is the validated numeric task ID, not the free-form logbook slug claimed by the finding.
- Pre-fix regressions proved that policy receipts accepted uppercase and whitespace-padded identities, assurance accepted non-canonical evidence hashes, short `USER` and all `USERNAME` values leaked, and a recomputed receipt with an uppercase current candidate was treated as superseded instead of corrupt.
- Policy-proof write/load boundaries and assurance evidence hashes now require exact 64-character lowercase hexadecimal identities. All candidate comparisons consume those validated identities and remain exact string comparisons.
- Public proof sanitization validates and redacts distinct `USER` and `USERNAME` values at token boundaries, including one- and two-character names, while preserving longer-token collisions.
- Focused tests passed for `internal/policyproof`, `internal/assurance`, `internal/proofbundle`, and `internal/completiongate`; `make test-fast` passed.

## Deviations
