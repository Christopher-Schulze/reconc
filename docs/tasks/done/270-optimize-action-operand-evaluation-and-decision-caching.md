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

- [x] Profile current predicate summaries, pointer traversal, and cache identity work
- [x] Add exact immutable value count/index/encoded-size primitives
- [x] Migrate pointer resolution and summaries without exposing backing storage
- [x] Thread one verified cache identity through evaluation and storage
- [x] Collapse duplicate expected-identity derivation
- [x] Precompile path-base and scalar-list comparison state
- [x] Remove the proven-dead `allowAbsent` parameter and local rune helper only in touched code
- [x] Add behavioral, allocation, benchmark, fuzz, and race coverage
- [x] Update action runtime and performance documentation
- [x] Run focused and complete repository verification

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
- Apple M1 baseline measurements: maximum legal evaluation used about 330 KiB
  and 810 allocations; 128 context-root predicates used 21,816 B and 259
  allocations; cache hit/miss used 27/24 allocations.
- After implementation, context-root predicates use 312 B and 3 allocations,
  maximum-depth pointer summaries use 0 B and 0 allocations, the calibrated
  maximum plan uses 806 allocations, and standalone cache hit/miss use 25/22
  allocations. Prepared lookup/store do not repeat normalization.
- Verification passed: action and gateway unit suites, all 61 bounded fuzz
  targets, focused race tests, Windows cross-compilation, `make test-fast`,
  vet, Staticcheck, calibrated benchmark comparison, publication audit,
  self-hosting, and release-trust.

## Deviations

None.
