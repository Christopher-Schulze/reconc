# TASK 222: Close v0.9.7 verification and release-documentation gaps

## Why

A source-first audit of the completed v0.9.7 work found that all 46 accepted
TASKs from the original review produced real implementation commits, but three
archived TASKs overstate their task-specific verification. TASK 197 lacks the
promised strict-lockfile differential and fuzz coverage, TASK 199 lacks an
instrumented policy-source capture-count assertion, and TASK 201 lacks direct
tests and measurements for the generated JavaScript response buffer. The
candidate release notes also omit the major transactional, filesystem,
compiler, ingest, runtime, Stop, and hook-worker changes delivered after
v0.9.6, while TASK 221 records self-invalidating publication-audit counts.

## Acceptance

- Strict lockfile decoding has differential, migration, fuzz, and benchmark
  coverage that proves duplicate-key, Unicode, depth, number, root-shape, and
  trailing-data behavior remains fail closed while cached typed parts are
  reused.
- Stop-attempt snapshot tests instrument source-identity capture directly,
  prove one capture per explicit phase, prove mutation creates a distinct
  phase identity, and retain stable/dirty/concurrent benchmark coverage.
- The exact generated JavaScript worker buffer is tested with one-byte and
  irregular chunks, exact-limit and overflow inputs, cancellation, restart,
  shutdown, and remainder handling; a benchmark reports bounded geometric-copy
  behavior rather than process startup cost.
- Generated OpenCode, Kilo, OMP, and Pi scaffold adapters remain derived from
  the canonical worker source and are byte-synchronized.
- The v0.9.7 release notes truthfully summarize every material improvement
  block since v0.9.6, preserve candidate-only wording, and state compatibility
  and verification limits precisely.
- TASKs 197, 199, 201, and 221 contain accurate final evidence without volatile
  counts that become false when the documentation commit is created.
- Focused tests, fuzz seeds, benchmarks, race tests, publication audit,
  documentation checks, complete repository gates, and release verification
  pass from the final source tree.

## Sub-Tasks

- [x] Add strict lockfile differential, migration, and fuzz verification
- [x] Add instrumented stop-attempt capture-count and mutation verification
- [x] Add direct worker-buffer behavior and performance verification
- [x] Synchronize generated adapters and complete release documentation
- [x] Run focused, race, publication, complete, and release gates
- [x] Reconcile archived TASK evidence and archive TASK 222

## Notes

- The original accepted review scope is TASK 174 through TASK 219 inclusive:
  46 archived TASKs, 46 implementation commits, and 46 commits with production
  code changes. TASK 220 and TASK 221 are follow-up publication and audit work.
- Before this TASK, 43 of the 46 original TASK commits changed task-specific
  test files. Existing aggregate suites cover parts of TASKs 197 and 199, but
  they do not satisfy the explicit archived acceptance claims. TASK 201 has no
  direct JavaScript buffer test or benchmark.
- The fresh pre-TASK publication audit passed over 1,249 tracked files, 212
  post-boundary commits, and 3,329 post-boundary blobs. These volatile counts
  are audit context only and must not be presented as immutable final evidence.
- Strict-decoder differential and migration tests passed, and the new fuzz
  target passed 500 executions as part of all 54 discovered root-module fuzz
  targets. The portable template defines no fuzz target.
- The exact generated JavaScript buffer contract passed under Bun 1.4.0. Its
  focused benchmark reported 128,281 copied bytes per full 128 KiB frame and a
  separate JavaScript-only time metric, excluding Bun startup.
- Go 1.26.7 passed both module tidy diffs, vet, pinned staticcheck, the complete
  root and portable-template race suites, publication audit, harness-pack,
  release-trust, and self-hosting gates. Both govulncheck scans reported no
  vulnerabilities.
- The hash-pinned external LangChain proof passed on Python 3.13.14, and the
  complete five-target v0.9.7 release matrix produced and verified binaries,
  manifest, checksums, SBOMs, notices, schema assets, completions, and manpage.

## Deviations

None.
