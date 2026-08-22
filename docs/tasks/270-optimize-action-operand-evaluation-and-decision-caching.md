# TASK 270: Optimize action operand evaluation and decision caching

## Why

Action predicates still serialize selected values to measure operand bytes and
clone arrays for a single JSON-pointer hop. Decision cache storage recomputes
the complete normalized cache identity already present in the evaluation
result, and identity construction calls `expectedIdentities` twice. Compiled
path constraints also renormalize their immutable base on every match. These
costs multiply by rule and predicate count on the MCP call hot path.

## Acceptance

- Immutable `action.Value` exposes allocation-free read-only count/index and
  exact canonical encoded-size access needed by pointer summaries. Mutable
  backing slices/maps remain unreachable and all JSON limits stay enforced.
- JSON-pointer array traversal uses `ArrayItem` or an equivalent bounded
  accessor and performs no full-array clone per token.
- Operand summaries report exactly the same kind, canonical byte length, item
  count, pointer state, and privacy-safe output for every scalar/container and
  malformed/unavailable state.
- Cache lookup/evaluation/store compute one normalized identity per logical
  evaluation. `Store` verifies that the supplied result is bound to the exact
  eligible identity without re-normalizing the entire input.
- `cacheIdentityWithReason` derives repository-effect and inspection identities
  once. Failure results remain explicitly non-cacheable and retain any identity
  fields required by explanation/audit contracts.
- Compiled path constraints retain normalized base, volume, case-folded form,
  and prefix once; only the runtime operand is normalized per evaluation.
- Compile-time scalar-list ordering precomputes canonical sort keys instead of
  marshaling inside the comparator, without changing deterministic order.
- Existing action fuzz targets and canonical JSON/schema tests are unchanged in
  meaning. Benchmarks show the effect on context-root predicates, maximum legal
  plans, pointer depth, and decision-cache hit/store paths.
- Documentation, benchmark history, race tests, and complete gates pass.

## Sub-Tasks

- [~] Profile current predicate summaries, pointer traversal, and cache identity work
- [ ] Add exact immutable value count/index/encoded-size primitives
- [ ] Migrate pointer resolution and summaries without exposing backing storage
- [ ] Thread one verified cache identity through evaluation and storage
- [ ] Collapse duplicate expected-identity derivation
- [ ] Precompile path-base and scalar-list comparison state
- [ ] Remove the proven-dead `allowAbsent` parameter and local rune helper only in touched code
- [ ] Add behavioral, allocation, benchmark, fuzz, and race coverage
- [ ] Update action runtime and performance documentation
- [ ] Run focused and complete repository verification

## Notes

- Current evidence: `summarizePointer` calls `Value.MarshalJSON`, `Items`, and
  `Members` for every evaluated predicate.
- Current evidence: `resolvePointerTokens` calls the copying `Items` method even
  though TASK 266 added `Value.ArrayItem` for safe indexed traversal.
- Current evidence: `DecisionCache.Store` calls `CacheIdentity` after evaluation
  already placed the same identity in `result.Cache`; `cacheIdentityWithReason`
  invokes `expectedIdentities(input)` twice.
- TASK 266 already bounded trace retention and migrated action-inspection
  walkers to indexed access. Do not reopen those completed changes.
- Exact trace byte accounting still requires canonical encoding of retained
  trace entries. Replacing it with an estimate is out of scope unless exact
  equivalence is proven for all escaped JSON forms.

## Deviations

None.
