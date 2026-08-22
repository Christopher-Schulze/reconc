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

- [~] Profile residual engine, detector-policy, scanner, and evidence-pack allocations
- [ ] Compute selected-field value identities once per pointer state
- [ ] Add an immutable non-copying detector-policy iteration boundary
- [ ] Implement bounded stack-first distinct-character counting
- [ ] Reuse the canonical fact-selector membership set
- [ ] Evaluate and, only if equivalent, reuse report canonical bytes
- [ ] Add isolation, privacy, allocation, benchmark, fuzz, and race tests
- [ ] Update inspection/evidence performance documentation and history
- [ ] Run focused and complete repository verification

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

## Deviations

None.
