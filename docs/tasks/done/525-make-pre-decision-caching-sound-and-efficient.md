# TASK 525: Make pre-decision caching sound and efficient

## Why

The pre-decision cache binds exact tool identity, policy, session state, evidence
chain, taint state, aliases, and selected path snapshots. A pre-command
composite can additionally evaluate fresh files, evidence files, scripts and
their cache inputs, or assurance state after its command trigger matches. Those
external dependencies are absent from the current cache identity, so an exact
`ToolUseID` retry can reuse a decision after a relevant input changes.

The cache also stores any stdout-free exit code 0 or 2 without distinguishing a
policy decision from an operational failure, and it performs a complete
post-evaluation resample even after earlier logic marks the operation
uncacheable. Existing measurements confirm material duplicate identity work,
but a metadata-only shortcut would be unsafe. This task consolidates audit
candidates C221, C222, C224, and C225.

## Acceptance

- Derive a deterministic, bounded dependency plan from the exact compiled rules
  reachable for the current pre-decision route, including dependencies nested
  through `all_of`, `any_of`, and `not`.
- Bind every filesystem or state input that can alter the decision, including
  `require_fresh_file` targets, `require_evidence` sources, `require_script`
  scripts and declared `cache_inputs`, assurance manifests/layout evidence, and
  any other reached rule-specific external input.
- Conservatively disable caching when dependency reachability, path expansion,
  file identity, evaluation phase, or total dependency bounds cannot be proven.
  Never omit a dependency merely to retain a cache hit.
- Preserve exact binding to session ID, tool-use ID, tool name/input, policy lock
  and source generation, normalized session state, evidence chain, taint state,
  Git aliases, prospective paths, and native-approval cache exclusions.
- Detect content replacement even when size and modification time are
  unchanged. Do not replace content or stable-generation identity with an
  `mtime+size`-only key.
- Keep the before/after mutation barrier required to prevent a concurrently
  changed input from validating or warming a stale record. Reuse immutable
  observations within one sample only where identity and lifetime are proven.
- Introduce an internal decision classification so only successfully evaluated
  policy Pass or Block/Fix results are cacheable. Parse, IO, cancellation,
  lockfile, source-freshness, script-execution, assurance, encoding, and other
  operational errors must never warm the cache.
- Move the uncacheable guard before post-evaluation resampling. Native approval,
  unsupported payloads, over-budget dependencies, and any other uncacheable
  route must perform zero cache-only post-samples.
- Preserve atomic private cache publication, bounded record and diagnostic size,
  deterministic encoding, exact retry scope, and fail-closed behavior for
  corrupt or stale records.
- Add regressions that mutate every dependency class between identical
  `ToolUseID` calls, including same-size/same-mtime replacement, symlink or
  ancestor replacement, policy generation, evidence segments, script inputs,
  assurance manifests, aliases, and session state.
- Add operational-error regressions proving that a subsequent healthy identical
  retry evaluates live and is not served the previous failure.
- Extend benchmarks to report identity samples, content-hash passes, bytes read,
  allocations, and end-to-end hit/miss cost for small and maximum bounded
  dependency sets. Demonstrate lower redundant work without relaxing identity.
- Pass focused cache/runtime/assurance/script tests under the race detector and
  all complete repository gates.

## Sub-Tasks

- [x] Read the complete pre-decision cache call graph, runtime rule indexes, every rule evaluator dependency, session/evidence caches, path identity helpers, alias snapshot, native approvals, and all cache tests/benchmarks.
- [x] Define one typed cacheability and decision-classification contract covering success, policy denial, operational error, cancellation, over-budget input, and approval-sensitive routes.
- [x] Build a conservative compiled dependency plan for reached top-level and nested rules, reusing existing canonical path expansion and source identities.
- [x] Bind the dependency plan into initial and post-evaluation snapshots; disable caching atomically whenever complete observation is unavailable.
- [x] Prevent operational results from warming cache and move all uncacheable checks ahead of cache-only resampling.
- [x] Add deterministic mutation, replacement, error-recovery, concurrency, corruption, and boundary regressions for every identity component.
- [x] Extend and run benchmarks for small/large hits and misses; optimize only redundant work proven safe by the dependency and mutation tests.
- [x] Update cache architecture, bounds, and threat-model documentation with the exact new identity and error-classification rules.
- [x] Run focused race/repetition tests and all required repository gates; verify the cache artifacts and re-read every modified file before archival.

## Notes

### Observed behavior

- `internal/runtime/agentsession/pre_decision_cache.go:388-420` collects session
  read/write paths and the pending write-tool paths. It does not inspect the
  reached compiled rules for external dependencies.
- `internal/runtime/evaluator_rules.go` and
  `internal/runtime/evaluator_composite.go` evaluate fresh-file, evidence,
  script, assurance, claim, and command checks after route triggering.
- `writePreDecisionCacheForPayload` accepts any stdout-free result with exit 0
  or 2. `Result` currently carries no cacheable policy-decision classification.
- The initializer at `pre_decision_cache.go:100` executes resampling before the
  `cacheable` condition is tested.

### Current measurement

- On Apple M1/darwin/arm64 with 20 iterations repeated three times, the existing
  small-repository benchmark measured one cache identity sample at
  approximately 0.73 to 0.87 ms/op and two samples at approximately 1.43 to
  1.52 ms/op.
- The benchmark proves duplicate bounded work in its fixture. It does not prove
  the audit's broad percentage claims, maximum-dependency latency, or that a
  weaker identity is safe.
- The cache is scoped to exact SessionID and ToolUseID retries, not ordinary
  unrelated hook calls.

### Required identity model

- Payload identity.
- Policy lock bytes and validated source generation.
- Session state and complete evidence-prefix identity.
- Taint and Git-alias identity.
- Existing, missing, prospective, symlink, ancestor, generation, content, and
  relevant metadata identity for every decision dependency.
- Typed result provenance proving a completed policy evaluation.
- A second stable observation before returning or publishing cached state when
  required by the mutation barrier.

### Verification matrix

- Cache miss, valid hit, corrupt record, wrong version, wrong tool ID, and wrong
  session ID.
- Pass and block policy decisions; every operational error category.
- Direct and nested fresh-file, evidence, script/cache-input, and assurance
  rules; mixed composites and non-triggered rules.
- Existing/missing/replaced dependencies; equal metadata with changed bytes;
  directory and ancestor swaps; alias and policy drift.
- Minimum, typical, maximum, and over-budget path counts/bytes.
- Concurrent mutation before lookup, during evaluation, before publication, and
  during cached return.

### Implementation

- Cache version 4 derives a sorted, 2,048-path-bounded dependency plan from the
  evaluator-owned compiled plan and exact pre-command/pre-write route. Reached
  fresh-file, evidence, script, cache-input, template-capture, and future
  assurance inputs either enter the content-bound identity or disable reuse.
- Freshness outcome and earliest transition, sanitized script environment,
  full file content and generation, resolved target, missing suffix, ancestor,
  sealed evidence, session, taint, policy, payload, and Git aliases remain
  inside the before/after mutation barrier.
- `CheckReport.CacheableDecision` and the private hook result class admit only
  completed policy pass/block decisions. Folded script and scope failures,
  parse/IO/cancellation/source/approval/encoding failures, contradictory cache
  records, and untyped results cannot publish reusable state.
- An uncacheable route now skips candidate reads and cache-only post-sampling.
  Existing identity work remains intact for cacheable hits and misses.

### Benchmark evidence

- Apple M1, darwin/arm64, Go benchmark `-benchtime=1x`: one external dependency
  plus one session write path measured 29.12 ms hit and 43.44 ms miss, with two
  identity samples, four content-hash passes, and 28 content bytes per operation.
- The maximum cacheable fixture uses 2,047 rule dependencies plus one session
  path. It measured 361.46 ms hit and 373.53 ms miss, with two identity samples,
  4,096 content-hash passes, and 8,212 content bytes per operation. These are
  local boundary measurements, not latency guarantees.
- Focused `go test ./internal/runtime ./internal/runtime/agentsession -count=1`
  passed after the implementation (22.96 s and 53.50 s respectively).

### Final verification

- Focused pre-decision tests passed uncached, under the race detector, and for
  five consecutive repetitions.
- `make test-fast`, `make test`, `make vet`, `make lint`, `make coverage`,
  `make build`, `make self-host`, and `make publication-audit` passed. Coverage
  remains whole-module review evidence, not a numeric pass/fail contract.
- Historical TASK 525 measurements recorded in
  `cb71a3b7a33bd651da95875632dfbdb422ef4aff`: whole-module coverage was
  82.3484% for the root module and 84.0628% for the portable template module.
  These values describe that task's verification run, not the current checkout.
- The final Go sources are `gofmt`-clean, `git diff --check` passes, and every
  modified file and task acceptance item was re-read before archival.

## Deviations

None.
