# TASK 243: Correct policy compiler and parser contracts

## Why

The authoring parser, static conflict detector, migration pipeline, and lockfile
diff reader currently disagree on legal modes, document ownership, list
validity, duplicate `require_read` semantics, and digest ownership. These are
deterministic policy-contract errors: valid artifacts can be rejected while
invalid or misleading authoring input can compile.

## Acceptance

- Lockfile diff accepts every `policy.Mode.Valid` value and rejects every other
  value through the same canonical mode contract as parser and runtime.
- Exact duplicate `require_read` detection compares its complete semantic
  requirement: normalized `paths` plus normalized `before_paths`; trigger-only
  or absent fields cannot produce false positives.
- `default_mode` is accepted only in compiler configuration. Policy fragments,
  presets, inline sources, and other source kinds reject it explicitly.
- Every contain-list entry is a string whose trimmed value is non-empty.
  Normalization behavior is intentional and identical across commands, claims,
  paths, arguments, cache inputs, and evidence content lists.
- Exactly one layer owns final migrated `lock_digest` stamping. Every migration
  step produces deterministic unstamped payload state or the driver avoids a
  redundant second full digest, with all legacy migration vectors unchanged.
- Parser, conflict, migration, schema, lockdiff, fuzz, and differential tests
  cover positive and negative cases for every corrected contract.
- Authoring and migration documentation describes the same accepted modes,
  field ownership, duplicate semantics, and digest boundary as code.

## Sub-Tasks

- [ ] Centralize lockdiff mode validation on the canonical policy enum
- [ ] Define and test the semantic duplicate key for `require_read`
- [ ] Reject misplaced `default_mode` before rule parsing
- [ ] Make contain-list whitespace validation consistent and exhaustive
- [ ] Assign migrated lock-digest stamping to one layer
- [ ] Add regression, fuzz, schema, and compatibility coverage
- [ ] Update policy authoring and migration documentation

## Notes

- External findings: F-1, F-2, F-4, F-5, and F-10.
- F-12 is cleanup-only and belongs to TASK 253.
- The duplicate key must not be reduced to `paths` alone: `before_paths`
  changes the read-order obligation and therefore changes rule semantics.
- Migration outputs must stay byte-compatible except for eliminating redundant
  work; fixture digests and all supported legacy formats are the acceptance
  oracle.

## Deviations

None.
