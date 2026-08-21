# TASK 231: Reuse evidence-path root identity

## Why

Evaluation input normalization resolves the repository filesystem identity
once, but the resolved value is discarded before rule evaluation. Every
`require_fresh_file`, `require_evidence`, and composite evidence path then calls
`resolvePolicyFile`, which resolves the same repository root again. Rules with
many contexts or evidence files multiply symlink/reparse and prospective-path
work on the runtime hot path even though one evaluation has one immutable root
boundary.

## Acceptance

- One evaluation resolves and validates the repository root identity once and
  threads the exact resolved root into path normalization and every evidence
  path resolution.
- `AssertRuleByID`, full evaluation, kind-filtered evaluation, pre-command
  evaluation, and composite sub-checks use the same root-bound mechanism.
- A prospective-path resolver is reused only within one evaluation and only if
  its ownership and mutation semantics are safe; no resolver state is shared
  across goroutines or evaluator calls.
- Lexical escapes, absolute paths, symlink escapes, parent replacement,
  repository-root identity drift, missing leaves, and special files remain
  fail-closed with the existing typed error contracts.
- Evidence snapshot identity revalidation remains authoritative; root reuse
  must not convert a cached pathname into trusted file content.
- Instrumented tests prove one root-resolution operation for many evidence
  files and verify behavior parity for top-level and composite checks.
- Benchmarks cover stable evaluation with 1, 32, and the maximum practical
  evidence paths and report latency and allocations without global claims.
- Runtime race tests and Unix/Windows path-identity suites pass.

## Sub-Tasks

- [x] Extend normalized evaluation state with the resolved root identity
- [x] Add a root-bound policy-file resolver to `evalContext`
- [x] Migrate every evidence and composite call site atomically
- [x] Add resolution-count, escape, identity-drift, and parity tests
- [x] Benchmark the evidence hot path and run cross-platform runtime gates

## Notes

- Session finding: `#9`.
- Primary code: `internal/runtime/evaluation_metrics.go`,
  `internal/runtime/evaluator.go`, `internal/runtime/evaluator_composite.go`,
  and `internal/runtime/policy_path.go`.
- TASKs 188 and 189 already batch input-path and write-epoch resolution. This
  TASK closes the remaining evidence-file path without introducing a second
  root cache.
- The logical repository path used in reports may remain distinct from the
  resolved filesystem identity used for containment checks.
- Normalization now owns an `evaluationPathState` containing the exact resolved
  root, frozen root file identity, and one evaluation-local prospective
  resolver. Read paths, write paths, epochs, top-level evidence, and composite
  evidence consume that same state.
- The root entry is `os.SameFile`-revalidated before evidence resolution and at
  the evaluation boundary. The prospective resolver independently revalidates
  cached parent entries, while `evidenceSnapshotCache` remains authoritative
  for evidence-file identity, metadata, and bounded bytes.
- Instrumented full-plan coverage proves one injected root-resolution call for
  32 top-level evidence files plus fresh-file and composite checks. Public
  assertion, kind-filtered, pre-command, and complete evaluation paths retain
  pass parity; root replacement, parent symlink replacement, and escape cases
  fail closed.
- Apple M1 benchmark medians over three 300-ms samples were approximately
  162 us/449 KB/351 allocations for one evidence path, 3.77 ms/695 KB/3,106
  allocations for 32, and 25.6 ms/2.51 MB/22,850 allocations for the parser
  limit of 256. These are local full-evaluation measurements, not global
  performance claims.
- Verification: full runtime and pathidentity tests, focused runtime race
  tests, and `go vet` passed. Darwin path-identity tests executed; Windows
  amd64 runtime and pathidentity test binaries cross-compiled successfully.

## Deviations

None.
