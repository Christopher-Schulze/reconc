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
- Pre-decision policy/source identity work is shared with runtime plan
  preparation only when both consume the exact same stable source bundle;
  before/after TOCTOU checks remain independent.
- Best-effort Stop-decision logging surfaces append failure through bounded
  diagnostics/metrics without turning a previously allowed hook result into an
  uncontracted hard failure.
- Benchmarks report cold Stop, cache hit, dirty large worktree, segmented
  evidence, packed refs, and unstable retry. Race/leak/corruption tests and
  complete gates pass.

## Sub-Tasks

- [ ] Diagram every current Stop capture and its exact stability purpose
- [ ] Introduce immutable capture bundles for the three trust boundaries
- [ ] Rewire cache lookup, evaluation, retry, and generation publication to reuse bundles
- [ ] Add identity-checked verified-prefix caching for evidence segments
- [ ] Stream packed-ref lookup within strict limits
- [ ] Add efficient write-path membership while preserving canonical state order
- [ ] Share pre-decision source work only through a common stable bundle
- [ ] Surface best-effort logging failures through bounded diagnostics
- [ ] Add cold/hit/dirty/segmented/unstable benchmarks and adversarial tests
- [ ] Update Stop cache, evidence-chain, and performance documentation
- [ ] Run race, leak, self-host, publication, and complete repository gates

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

## Deviations

None.
