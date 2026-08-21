# TASK 203: Make policy-source precedence truthful and canonical

## Why

`SourceCustomRuntime` exists but is omitted from `SourcePrecedence` and ranked
through a special case elsewhere. More importantly, the policy API documents
that higher-precedence sources win while the parser rejects every duplicate
rule ID regardless of source tier. The current implementation and contract
cannot both be true.

## Acceptance

- One explicit product decision defines whether cross-tier duplicate IDs are
  overrides or errors; implementation, docs, schemas, compiler envelope, and
  runtime validation all express that same decision.
- Every source kind, including custom runtime, has one canonical rank without
  ad hoc ranking branches.
- Same-tier and cross-tier duplicate diagnostics identify both source locations
  and remain deterministic.
- Lockfile/source-precedence compatibility is migrated explicitly if its
  serialized list changes.
- Exhaustive tests cover every source-kind pair and duplicate direction.

## Sub-Tasks

- [x] Decide and document duplicate-ID precedence semantics
- [x] Canonicalize the complete source-kind order
- [x] Implement parser/compiler/runtime behavior consistently
- [x] Add compatibility and exhaustive precedence tests
- [x] Run policy, ingest, parser, compiler, runtime, and complete gates

## Notes

- Evidence: `internal/policy/types.go:406-443`, source ranking in
  `internal/ingest/candidate.go`, and duplicate handling in
  `internal/parser/parser.go`.
- The old session's runtime-rejection scenario was hypothetical; this TASK is
  based on the verified contract divergence.

## Deviations

None.
