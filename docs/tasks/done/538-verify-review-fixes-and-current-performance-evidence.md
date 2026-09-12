# TASK 538: Verify review fixes and current performance evidence

## Why

The six repairs need a final integrated proof and current comparable performance measurements; the prior full suite predates TASK 525 and Cursor implementation.

User-approved review item: Completion. Execute on main; commit and push after completion. Product tags and releases remain outside this task.

## Acceptance

- All six reported defects are closed with meaningful regression tests.
- Integrated repository gates pass and benchmark results identify clean source revisions and comparable environments.
- Performance claims distinguish per-operation allocation metrics from process peak memory and disclose any remaining regression.
- Historical details remain preserved, the overview follows the ten-entry contract, and remote state matches the committed deliverables.

## Sub-Tasks

- [x] Verify every R1-R6 acceptance against committed code, regression evidence, archived details, and remote commit identities.
- [x] Run the integrated test, vet, lint, development build, whole-module coverage, isolated self-host, and publication checks.
- [x] Record the complete benchmark suite for a clean code candidate and comparable baseline with attributable source and environment; assess time, allocated bytes, allocation count, and process peak memory separately.
- [x] Investigate measured regressions before claiming completion; preserve truthful evidence and avoid changing thresholds merely to make results green.
- [x] Keep only ten Done entries, verify retention of all 25 ignored original historical details, and explicitly document their local status without publishing private planning material.
- [x] Flush task-relevant documentation, re-read final files, archive, commit, push origin/main, and verify the persisted remote head.

## Notes

- Starting source: 70117feaa45b85d045670219d598fe9d1f1428e8.
- Source boundaries: Makefile; scripts/benchmarks/history; docs/documentation.md; docs/tasks.md.
- Use isolated repository fixtures; never run repository-targeted Reconc commands against the product root.
- Planning details are explicitly tracked for this approved work despite the existing local-task ignore rule.
- R1-R6 are committed and pushed in TASKs 532-537: `9bea6967435dd8d2092ade70a425018e90452419`, `ebae57b557650968b5c70d04091fbe17b37e084f`, `dd80e73b7f30e34ac61c145a9f128c3192513ce9`, `838372707a8d758854c2bf6aefc06e31d84c1fa8`, `003f73311dfbcc8c4e969aecc4b9b02551a5f8c3`, and `7c5a666ae11d84cd3e348418267f9781706c97c3`. Their archived details identify acceptance and regression evidence; each remote head was checked after push.
- Remote CI at TASK 535 (`34693996802`) fails only the Linux Gitlink coverage fixtures: cloned submodules do not inherit their source repository's author configuration. Both affected tests reproduce with global configuration disabled and `user.useConfigOnly=true`; an isolated overlay proves the local clone-configuration correction. Retain that restricted environment inside both tests to prevent developer configuration from masking regression.
- Benchmark source candidate: clean detached local checkout `7c5a666ae11d84cd3e348418267f9781706c97c3`, containing all six fixes. TASK 538 changes test fixtures and evidence only, not the benchmarked production implementation. Baseline: clean exact checked source `c814f5b40579d2feed47231ec9dc2d80afda1dea`. Both checkouts are inside ignored `.build/benchmarks`; the product checkout stays on main.
- Record all 19 groups and 24 targets with the checked five samples, three repetitions, CPU 1, and 250ms benchtime. Use alternating baseline/candidate runs and unchanged checked tolerances; wait for heavy verification work to finish before measurement.
- All 25 original historical detail files match `.build/review-repairs/original-history-hashes.json`, remain ignored, and are not tracked. Recheck before final commit; publish only explicitly approved task details.
- TASK 536's ordinary blocked-budget checkpoint continuation revalidates retained history. That exceptional path is not exercised by the existing checkpoint-advance microbenchmarks; do not infer its latency from their results.
- Integrated verification passed: complete uncached root and template race suites, release trust, vet, Staticcheck, development build, isolated self-host, and publication audit. Evidence: `.build/review-repairs/task538-full-test.log`, `task538-vet.log`, `task538-lint.log`, `task538-self-host.log`, and `task538-publication.log`.
- Whole-module coverage measured 82.5247% for the root module and 84.0628% for the portable template on macOS. Profile hashes and the exact uncommitted test-fixture paths are recorded in `.build/review-repairs/task538-coverage-provenance.json`; the production code matches the clean benchmark candidate.
- The complete comparable benchmark run finished successfully; comparison correctly exits with regression status. 22 of 24 targets pass all limits. Warm-prefix allocated bytes increased from 643342 to 684886 B/op and allocations from 5585 to 5906; cold-prefix increased from 725409 to 766701 B/op and from 6152 to 6470 allocations. All time and process peak-RSS limits pass. Four normalized allocation metrics exceed their unchanged five-percent limits; the corresponding absolute metrics stay within their ten-percent limits.
- Full data: `.build/benchmarks/task538-runner-result.json`, `task538-current.json`, `task538-runner-baseline.json`, and `task538-comparison.json`. Both source identities are clean; the checked baseline hash is unchanged. Result hashes are retained in `.build/benchmarks/task538-result-hashes.json`.
- Attribution completed: five pre-repair runs at `70117feaa45b85d045670219d598fe9d1f1428e8` reproduce the current allocation counts. Three fixed-work runs before/after TASK 525 identify the increase at `cb71a3b7a33bd651da95875632dfbdb422ef4aff`; exact allocation profiles attribute approximately 3991 KiB of additional cumulative allocation to PreDecisionDependencies, approximately 3889 KiB of it to input normalization. Profile totals include setup and repeated work and are not per-operation values.
- The remaining performance work is explicitly queued as TASK 539. TASK 538 completes measurement and attribution; it does not claim that the benchmark comparison passes. No tolerances were relaxed, no checked baseline was refreshed, and no performance-reducing identity shortcut was introduced.
- Remote CI for TASK 537 (`34695752354`) again confirms only the two inherited Gitlink author failures; its CodeQL run succeeds. Final CI is checked after this task's push, separately from local gate evidence.

## Deviations

None.
