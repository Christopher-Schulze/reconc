# TASK 510: Run the complete bounded profile pipeline in CI

## Why

The benchmark workflow records a full result but its profile step runs one
microbenchmark with only CPU and memory profiles, ignores failure, and stores no
manifest, blocking, mutex, or trace evidence. The repository already owns a
bounded multi-workload `make benchmark-profile` pipeline that records all of
those artifacts with source and hardware metadata.

## Acceptance

- Pull request, scheduled, and manual benchmark runs execute `make benchmark-profile` with the bounded workload groups.
- Profile failures fail the job; artifact upload still runs and retains result, comparison, manifest, and all profile kinds.
- The workflow has an explicit bounded timeout and no direct single-microbenchmark profile workaround.
- A publication contract test prevents regression to an ignored or incomplete profile step.

## Sub-Tasks

- [x] Compare the workflow with the actual `make benchmark-profile` and profile manifest contract.
- [x] Replace the ignored microbenchmark step with the complete bounded pipeline and explicit artifact path.
- [x] Add workflow contract coverage and documentation evidence.
- [x] Run affected tests and repository gates, archive this detail, and push one TASK commit.

## Notes

### Review provenance

- Follow-up to the TASK 490/491 audit: `.github/workflows/reconc-benchmarks.yml` bypassed the existing profile orchestration and retained incomplete evidence.
- Native AMD64 performance remains unavailable on this ARM64 host; this task changes CI evidence collection only.

### Implementation and verification

- The workflow now runs `make benchmark-profile BENCHMARK_PROFILE_DIR=.build/benchmarks/profiles` as a required step on `macos-15` with a 30-minute timeout. The old single `BenchmarkPreparedDecisionCacheHit` command and `continue-on-error` path are gone.
- Artifact upload remains `if: always()` with 30-day retention and covers `.build/benchmarks/`, including current result, comparison, profile manifest, CPU, heap, blocking, mutex, and trace files.
- `TestBenchmarkWorkflowRunsCompleteBoundedProfilePipeline`, `go test ./scripts/audits/publication ./scripts/benchmarks/history -count=1`, `make test`, `make vet`, `make lint`, `make self-host`, `make reference-docs-check`, and `git diff --check` passed.

## Deviations

None planned.
