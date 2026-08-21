# TASK 236: Correct internal contract documentation

## Why

Several internal comments contradict the code they are meant to protect.
`marshalCanonical` says whitespace is stripped although `json.Marshal` already
emits compact JSON and no stripping occurs. `ruleToMap` contains unfinished
self-correction prose. The safety-critical assurance-gate copy does not explain
that it detaches mutable nested command slices. `privatefs.EnsureDirectory`
claims every existing path component is reconciled to private mode, while the
implementation secures missing components and validates the final boundary but
does not chmod arbitrary existing ancestors. Incorrect comments are dangerous
at identity and filesystem-security boundaries because later changes may rely
on guarantees the implementation never made.

## Acceptance

- Canonical JSON comments state the exact guarantees supplied by
  `json.Marshal`: compact output, deterministic string-key ordering, and error
  propagation, with no fictitious whitespace-removal phase.
- `ruleToMap` documentation describes omission of empty optional fields and the
  resulting identity contract directly, without historical or unfinished
  implementation narration.
- Assurance-gate conversion documents slice ownership and explains why nested
  command slices are copied when they leave the immutable runtime plan for
  evaluation.
- `EnsureDirectory`, `RepairDirectory`, and `SecureDirectory` comments
  distinguish created components, existing ancestors, final-boundary mode
  validation, repair behavior, identity checks, and platform ACL checks exactly.
- User-facing architecture or security documentation is updated only where it
  repeats a corrected false guarantee; no new behavior is claimed.
- Comment examples and identifiers match current function signatures and
  current Go 1.27 implementation.
- A source review verifies every changed comment against the corresponding
  body and callers; documentation tests, formatting, vet, and affected package
  tests remain green with no production-code change unless needed to expose an
  already-existing invariant safely.

## Sub-Tasks

- [x] Audit each identified comment against its implementation and callers
- [x] Correct canonical JSON and rule-serialization contract text
- [x] Document assurance-gate deep-copy ownership at the copy boundary
- [x] Correct private-directory creation, validation, and repair guarantees
- [x] Search user-facing docs for duplicated false claims and reconcile them
- [x] Run documentation, compiler, runtime, and privatefs verification

## Notes

- Session findings: `#2`, `#15`, `#20`, and `#21`.
- Primary code: `internal/compiler/compiler.go`,
  `internal/runtime/runtime_plan.go`, `internal/runtime/evaluator.go`, and
  `internal/privatefs/privatefs.go`.
- Documentation must describe current target-state behavior only. Do not add a
  historical changelog or reproduce the audit findings in product docs.
- Strengthening `EnsureDirectory` to chmod all ancestors is explicitly outside
  scope and could damage user-owned directories such as home-directory parents.
- `marshalCanonical` now documents only `json.Marshal` guarantees: compact
  bytes, deterministic string-key ordering, and direct error propagation.
  `ruleToMap` now states the optional-field omission and serialized-identity
  contract without implementation-history narration.
- `assuranceGatesFromRule` documents that gate structs leave the immutable
  runtime plan by value while each mutable `Commands` backing array is cloned.
  A regression test mutates both returned layers and proves the cached rule is
  unchanged.
- The runtime-plan ownership comment and construction path already matched the
  decoded-plan lifetime and required no change.
- Private-directory comments and architecture now distinguish created private
  components, identity-only checks for existing ancestors, fail-closed final
  validation, explicit final-boundary repair, and platform security checks.
  A Unix contract test proves `EnsureDirectory` leaves ancestor mode unchanged,
  rejects final mode drift without repairing it, and `RepairDirectory` changes
  only the final boundary.
- Verified with focused and race tests for compiler, runtime, and privatefs;
  affected `go vet`; root `go mod tidy -diff`; repository and portable pinned
  Staticcheck `v0.8.0`; format and diff checks; and a Windows/amd64 privatefs
  test-binary compilation.

## Deviations

None.
