# TASK 469: Complete v0.9.8 Windows and release gate

## Why

The unpublished `reconc-v0.9.8` candidate still has two native Windows
failures. Atomic-file identity replacement tests pass a path-lazy `os.Lstat`
identity across a replacement, and the JSONL stress test can exhaust the
default ten-second lock budget under the Windows runner's serialized rotating
append workload. The candidate must also include the completed OMP 18
integration and be proven by the deep CI, security, release, and artifact
workflow before publication.

## Acceptance

- Atomic-file identity replacement regressions use an identity captured from
  an opened handle before the path is replaced and continue to fail closed
  against the replacement on Windows.
- Concurrent JSONL append coverage retains rotation, bounded archives, and
  whole-record validation while using an explicit stress-test lock budget;
  production lock semantics and all existing timeout tests remain unchanged.
- Release notes explicitly cover the OMP 18.0.11 contract integration,
  generic compaction events, per-binding worker ownership, and host-controlled
  extension loading.
- Focused Windows-related tests, `make test-fast`, `make test`, `make vet`,
  `make lint`, `make reference-docs-check`, and `git diff --check` pass.
- All TASK sub-tasks are complete, the detail file is archived, and exactly
  one local commit uses the format `TASK 469: <title>`.
- The exact committed source passes push-triggered Linux/macOS CI, full native
  Windows CI, CodeQL, the exact-tag release workflow, release-trust,
  self-host, artifact checksums, SBOM/notices, and provenance verification.
- `reconc-v0.9.8` points to the verified commit, the GitHub release exists with
  its verified assets, and no unrelated files or tests are removed or disabled.

## Sub-Tasks

- [x] Reproduce and document the native Windows failures against the current
  source and exact failed workflow evidence.
- [x] Make atomic-file identity snapshots handle-backed and add the Windows
  regression proof.
- [x] Bound the JSONL concurrency stress fixture without weakening production
  locking or rotation assertions.
- [x] Update v0.9.8 release notes for the OMP integration and review all
  affected documentation and callers.
- [x] Run focused and complete local gates, inspect the full diff, archive the
  TASK, and create the single local commit.
- [x] Push the verified commit, run deep CI and CodeQL, retarget the protected
  v0.9.8 tag, publish the release, and verify all live artifacts and checks.

## Notes

- Release workflow `33333736042` failed on commit `8d2be0fb` in
  `TestWriteIfCurrentRejectsConcurrentIdentityReplacement`,
  `TestWriteStreamIfCurrentRejectsConcurrentIdentityReplacement`, and
  `TestAppendBoundedUnderConcurrency`; all other native packages completed.
- Go 1.27 Windows `os.Lstat` metadata may defer file-ID loading until
  `os.SameFile`, so an expected `FileInfo` must be captured from an open file
  handle before an adversarial path replacement.
- The current source commit is `0f19e2fc`; the local and remote v0.9.8 tag
  still dereference to `8d2be0fb`, and no GitHub v0.9.8 release exists.
- The focused atomic-file identity tests pass 20 consecutive runs, the JSONL
  rotating-concurrency stress passes five consecutive runs, and both affected
  packages cross-compile their test binaries for Windows amd64.
- The OMP scaffold change from TASK 468 had left the committed advanced harness
  pack stale; regenerating its manifest and archive produced 119 files,
  `866413` bytes, and digest
  `d187deb8edeb06be4d23d74db0052e07cc272f2e0ab31e207905cee02c447509`, after
  which `make publication-audit` passed.
- Final local gates passed after the fixes and pack regeneration: focused
  atomic-file tests (`-count=20`), JSONL concurrency stress (`-count=5`),
  Windows amd64 test-binary cross-compiles, `make test-fast`, `make test`
  (including race suites and release-trust), `make vet`, `make lint`,
  `make reference-docs-check`, and `git diff --check`.

## Deviations
