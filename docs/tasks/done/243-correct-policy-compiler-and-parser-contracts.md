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

- [x] Centralize lockdiff mode validation on the canonical policy enum
- [x] Define and test the semantic duplicate key for `require_read`
- [x] Reject misplaced `default_mode` before rule parsing
- [x] Make contain-list whitespace validation consistent and exhaustive
- [x] Assign migrated lock-digest stamping to one layer
- [x] Add regression, fuzz, schema, and compatibility coverage
- [x] Update policy authoring and migration documentation

## Notes

- External findings: F-1, F-2, F-4, F-5, and F-10.
- F-12 is cleanup-only and belongs to TASK 253.
- The duplicate key must not be reduced to `paths` alone: `before_paths`
  changes the read-order obligation and therefore changes rule semantics.
- Migration outputs must stay byte-compatible except for eliminating redundant
  work; fixture digests and all supported legacy formats are the acceptance
  oracle.
- Lockdiff now delegates all four legal values to `policy.Mode.Valid`.
  `require_read` duplicate detection compares normalized `paths` and
  `before_paths`, while all policy string-list readers reject whitespace-only
  elements and store trimmed values.
- Non-compiler sources, including impact candidates, reject `default_mode`
  before rule parsing. Runtime tests and benchmarks were corrected to place the
  field in `.reconc.yml` rather than weakening the production contract.
- Migration steps return unstamped target-version payloads. The driver stamps
  each intermediate and final digest once, preserving the authentication input
  required by the next step and removing the former redundant final pass.
- Verification: focused package tests and race tests passed for parser,
  compiler, lockdiff, schema, and runtime; both parser and migration fuzzers ran
  for two seconds without failure; corrected runtime benchmarks executed once;
  `make test`, `make vet`, `make lint`, `make self-host`, and module-tidy drift
  checks all passed.

## Deviations

None.
