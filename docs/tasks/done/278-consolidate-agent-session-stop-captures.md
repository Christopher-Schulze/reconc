# TASK 278: Consolidate agent-session stop captures

## Why

One Stop attempt reloads session state and its segmented evidence chain,
captures TASK state, captures Git state, and derives repository generation
multiple times across cache lookup, post-evaluation stability, and generation
publication. The repetitions are partly required TOCTOU boundaries, but several
captures within the same boundary are duplicated. Packed Git refs are split
into all lines at once, and complete evidence segments are reread and
reverified on repeated worker events.

## Acceptance

- Stop checking defines explicit `before evaluation`, `after evaluation`, and
  `before cache publication` capture bundles. Each bundle contains one coherent
  session/evidence revision, TASK snapshot, Git snapshot, policy identity, and
  dirty-file scan.
- Consumers reuse a bundle only inside its trust boundary. Required stability
  recaptures remain separate, and any mismatch invalidates the cache or retries
  up to the existing bounded stability limit.
- Verified evidence-segment caching stores only immutable, digest-bound prefixes
  and revalidates file identity, size, chain head, segment count, and newest
  state before reuse. Appended/replaced/corrupt segments fail closed.
- Packed refs are scanned incrementally within the existing byte/line bounds;
  the parser does not allocate one string per unrelated ref.
- Repeated write-path membership checks are O(1) or O(log n) without changing
  first-observation order, write epochs, state schema, or evidence fingerprints.
- Pre-decision policy/source identity work is shared only inside one explicit
  Stop capture. Runtime plan preparation remains independent because it owns a
  separately revalidated source bundle; no unproven cross-boundary reuse is
  added.
- Best-effort Stop-decision logging surfaces append failure through bounded
  diagnostics/metrics without turning a previously allowed hook result into an
  uncontracted hard failure.
- Benchmarks report cold Stop, cache hit, dirty large worktree, segmented
  evidence, and packed refs. A real mutating-script regression proves the
  three-attempt unstable retry bound. Race/corruption tests and complete gates
  pass.

## Sub-Tasks

- [x] Diagram every current Stop capture and its exact stability purpose
- [x] Introduce immutable capture bundles for the three trust boundaries
- [x] Rewire cache lookup, evaluation, retry, and generation publication to reuse bundles
- [x] Add identity-checked verified-prefix caching for evidence segments
- [x] Stream packed-ref lookup within strict limits
- [x] Add efficient write-path membership while preserving canonical state order
- [x] Share source identity only inside a proven common stable bundle
- [x] Surface best-effort logging failures through bounded diagnostics
- [x] Add cold/hit/dirty/segmented benchmarks and adversarial unstable-retry tests
- [x] Update Stop cache, evidence-chain, and performance documentation
- [x] Run race, leak, self-host, publication, and complete repository gates

## Notes

- Current evidence: `runStopPolicyCheckLockedAttempt` repeatedly calls
  `loadCurrentStopPolicyStateWithRevision`, `captureStopTaskSnapshot`, and
  `stopPolicyGitSnapshotFor`; `storeStopGenerationIfWorthwhileWithScan` captures
  TASK/Git state again before and after generation.
- Current evidence: `loadCompleteSessionEvidence` reopens and verifies all
  segments for each call; `readPackedGitRef` uses `strings.Split` over the
  complete bounded file.
- Full dirty-file hashing across separate stability runs is intentionally not
  removed. Metadata-only reuse could miss a content change with restored
  timestamps. Sharing is allowed only inside one already captured scan or via a
  stronger identity/content proof.
- Existing lock ordering, evidence taint, three-run stability bound, Git rename
  handling, submodule bounds, and hook result contracts are invariants.
- Capture map: `before_evaluation` owns the evidence revision actually
  evaluated; `after_evaluation` independently recaptures TASK, Git, policy, and
  dirty inputs; `before_cache_publication` recaptures the generation and is
  followed by the existing independent generation barrier. Input drift still
  retries at most three evaluations before failing closed.
- The wrapper and report-lock closure previously reconstructed segmented
  evidence before the attempt immediately loaded it again. The attempt now
  owns the single authoritative pre-evaluation load. Worker prefix caching
  reuses only decoded values after exact bytes and identity are revalidated and
  is bounded to 16 MiB aggregate.
- Runtime evaluator source loading was not merged with Stop fingerprint source
  loading. The evaluator exposes no stable source bundle, so conflating two
  separately observed bundles would weaken rather than optimize the mutation
  barrier.
- Best-effort run-decision append failures now produce a bounded stderr warning
  without changing the already computed allow/block control response.
- Verification: the complete `internal/runtime/agentsession` race suite passed;
  the real mutating-script test exhausted exactly three evaluations; exact,
  warm, large-worktree, linked-worktree, submodule, and concurrent generation
  benchmarks passed together with the new segment/prefix, packed-ref, and
  large-write benchmarks. Self-host, fmt, vet, Staticcheck, reference docs,
  publication-audit, and harness-pack checks passed.

## Deviations

None.
