# TASK 272: Reduce action inspection and evidence allocation

## Why

Action inspection computes a provisional selected-value identity and then
recomputes it from canonical bytes, deep-clones every matching compiled
detector policy, and allocates a rune map for each secret candidate. Evidence
pack validation rebuilds the complete known-selector set for every control.
These are smaller than the TASK 266 detector-pack and traversal wins, but they
remain repeated work in inspected MCP calls and evidence publication.

## Acceptance

- Present/null selected values compute one final value identity; missing and
  invalid pointer states retain their state-bound identity without duplicate
  keyed-HMAC work.
- Action inspection iterates matching immutable compiled detector policies
  without exposing plan-owned slices/maps to mutation and without deep-cloning
  each policy per request.
- Secret-candidate diversity counting avoids a heap map for the common ASCII
  path and remains Unicode-correct, bounded, and behaviorally identical.
- The immutable `AllFactIDs` membership set is constructed once or validated by
  direct indexed lookup, not rebuilt per control.
- Evidence report identity and final canonical validation reuse encoded work
  only where the two payload contracts are byte-identical. Required independent
  validation is retained and documented rather than removed speculatively.
- Allocation tests demonstrate no regression for binary/confusable scanning,
  selected-field identities, detector-policy isolation, evidence controls, and
  maximum legal reports.
- Existing privacy, identity-key, zeroing, schema, fuzz, race, docs, benchmark,
  and complete gates pass.

## Sub-Tasks

- [x] Profile residual engine, detector-policy, scanner, and evidence-pack allocations
- [x] Compute selected-field value identities once per pointer state
- [x] Add an immutable non-copying detector-policy iteration boundary
- [x] Implement bounded stack-first distinct-character counting
- [x] Reuse the canonical fact-selector membership set
- [x] Evaluate and, only if equivalent, reuse report canonical bytes
- [x] Add isolation, privacy, allocation, benchmark, fuzz, and race tests
- [x] Update inspection/evidence performance documentation and history
- [x] Run focused and complete repository verification

## Notes

- Current evidence: `internal/actioninspect/engine.go:inspectSelectedValue`
  computes `valueIdentity` from state and overwrites it with a body-bound
  identity for present/null values.
- Current evidence: `CompiledPlan.DetectorPolicies` deep-clones each matching
  policy, field list, and pointer-token list before every inspection.
- Current evidence: `likelySecretValue` creates `map[rune]struct{}` for every
  candidate; detector input length is already bounded to 512 bytes.
- TASK 266 already reuses one compiled detector pack and migrated the major
  structured JSON walkers to `ArrayItem`. Do not duplicate those tasks.
- Double canonicalization in action approval is security-motivated and out of
  scope. Do not collapse sign/verify boundaries merely to reduce allocations.
- The detector boundary now returns interfaces implemented by an unexported
  view. Callers can read scalars, memberships, forbidden terms, and resolved
  indexed fields but cannot obtain or mutate plan-owned slices or pointer
  tokens. The detached `DetectorPolicies` API remains unchanged.
- Report identity bytes and published report bytes are not byte-identical. The
  compact identity payload clears `identity`; final publication independently
  validates that digest and emits indented JSON. The second identity check is a
  deliberate security boundary and was retained.
- Apple M1, 100 fixed iterations: representative structured inspection uses
  4,944 B/op and 69 allocs/op versus the checked baseline's 6,384 B/op and 90
  allocs/op. ASCII diversity scanning and canonical selector validation are
  both allocation-free.
- Maximum-content inspection improved from the checked 10,733,285 ns/op,
  178,336 B/op, and 88 allocs/op medians to 9,598,411 ns/op, 176,912 B/op,
  and 68 allocs/op. The calibrated history entry was refreshed only for the
  changed action-inspection group; every absolute target metric improved and
  the normalized comparison passes.
- Verification passed focused package and race tests, 61 root fuzz targets,
  calibrated benchmark record/compare, `make test-fast`, Vet, Staticcheck,
  publication and release-trust checks, and the complete root/template
  `make test` race gate. Release-trust used temporary fixtures; no tag or
  release was created.

## Deviations

None.
