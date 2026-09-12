# TASK 539: Reduce session pre-hook allocations without weakening cache identity

## Why

TASK 538's complete comparable benchmark run found four remaining relative allocation regressions in the warm-prefix and cold-prefix session-hook workloads. The increase already exists before TASKs 532-537 and is attributable to TASK 525's dependency planning. The six review repairs must remain intact.

## Acceptance

- Both session-hook targets meet the existing relative byte and allocation limits against the exact checked baseline, with clean source identities and unchanged tolerances.
- Preserve every TASK 525 dependency, content/generation identity, root/path validation, operational-error exclusion, and before/after mutation barrier; do not substitute metadata-only identity or skip malformed input validation.
- Preserve TASK 533 declared prospective writes, alias and symlink retargeting, native approval exclusions, and completed-evidence separation.
- Meaningful cache/dependency/normalization regression tests, complete repository gates, and the full comparable benchmark suite pass; report time, per-operation allocation, and process peak memory separately.

## Sub-Tasks

- [x] Reproduce the committed TASK 538 measurements and inspect the allocation profiles at the exact TASK 525 boundary.
- [x] Read PreDecisionDependencies, normalizeEvaluationInput, preDecisionEvaluationInputs, capturePreDecisionObservedIdentityWithEvaluatorAndStopCache, and their callers and invalid-input contracts.
- [x] Remove redundant normalization or observation only where an existing validated immutable value can be reused within the same sample; evaluate empty-route handling without bypassing validation or the second observation barrier.
- [x] Add behavioral regressions for the chosen reuse boundary, including same-size/same-mtime changes, path replacement, stale sources, malformed declarations, dependency bounds, and operational failures.
- [x] Measure both session workloads and all 19 benchmark groups with unchanged parameters and limits; run repository gates, update relevant documentation, archive, commit, and push only under execution authority.

## Notes

- Execution, commit, and push explicitly authorized by Christopher. Starting clean source: `03439caf24b1a5ffb45d70d6da2a395f486215be`.
- Design: when the immutable route index is empty, retain complete read/write/epoch path normalization and final root revalidation, but omit unused command/result/claim normalization and rule-context allocation. Nonempty routes, filesystem snapshots, both observation barriers, and cache identity remain unchanged. No cross-sample reuse or new cache is needed.
- Five focused before/after benchmark samples reproduce the original regression and reduce warm-prefix allocation from approximately 684887 to 665283 B/op and 5906 to 5801 allocations. Cold-prefix drops from approximately 767084 to 747364 B/op and 6474 to 6367 allocations. These preliminary local samples are not the final comparable suite.
- New regressions exercise 72 empty-route path cases across empty and completion-only policies, both pre-hook routes, and read/write inputs; eight real filesystem/cache cases cover equal-metadata content changes, equal-content replacement, escaping links, and stale policy sources. An allocation-growth regression over the actual compiled evaluator fails against the original production file (115 versus 1166 allocations) and passes after the change.
- Focused race validation passed for both affected packages, including existing declared-write, malformed-declaration, dependency-capacity, operational-failure, and mutation-barrier cases: `.build/review-repairs/task539-focused-final.log`. The intentional old-code failure is retained in `task539-allocation-red.log`.
- Benchmark candidate is the clean detached local source snapshot `2544d996bbb637f4562e36e62075bf338e640d0f`, parented at the task's starting source, with exactly the three changed Go files. It exists only inside ignored `.build/benchmarks/task539-candidate-source`; the product remains on main. Final source bytes were verified against this measured snapshot before the task commit.
- Complete local gates passed: uncached race suites for both Go modules, publication audit, release trust (69-second real release fixture), vet, Staticcheck, development build, and isolated self-host. Logs: `.build/review-repairs/task539-full-test.log`, `task539-vet.log`, `task539-lint.log`, and `task539-self-host.log`.
- Whole-module coverage measured 82.5481% root and 84.0628% portable template. The full benchmark pair started only after these checks finished; source-file and checked-baseline hashes are retained in `.build/review-repairs/task539-source-provenance.json`.
- The complete comparable run passes: all 19 groups and 24 targets meet every existing governed time, allocation, and process peak-memory limit. Baseline and candidate are clean, immutable sources; parameters remain five samples, three repetitions, 250ms, and CPU 1. The runner-local baseline preserves the checked source and every tolerance; the checked baseline file is unchanged.
- Final warm-prefix measurement: 665122 B/op and 5800 allocations versus baseline 643342 B/op and 5585 allocations. Relative changes are +3.3852% bytes and +3.8496% allocations, below the unchanged 5% limits. Cold-prefix: 747158 B/op and 6367 allocations versus 725472 B/op and 6154 allocations; relative changes are +2.9890% and +3.4612%.
- CPU-adjusted median time changes are +2.4208% warm and +2.2394% cold against the historical baseline, within the existing 20% bounds. Both session targets have measured process peak RSS 19857408 bytes versus 22724608 bytes for this baseline run. These are process peaks, not per-operation allocations.
- Raw sample p95 remains reported rather than hidden: warm 2.8394ms versus 2.2684ms, cold 3.3017ms versus 2.6531ms. One slow candidate sample affects both targets and their evidence-prefix calibration; this observation alone does not establish a causal tail-latency regression. The existing time gate governs CPU-adjusted medians, not raw p95. No claim is made that every latency percentile improved.
- Full proof: `.build/benchmarks/task539-runner-result.json`, `task539-current.json`, `task539-runner-baseline.json`, and `task539-comparison.json`; hashes in `task539-result-hashes.json`. Coverage profile hashes are in `.build/review-repairs/task539-coverage-hashes.json`. All 25 original private historical details remain byte-identical, ignored, and untracked.

### Discovery evidence from TASK 538

- Checked baseline: `c814f5b40579d2feed47231ec9dc2d80afda1dea`. Clean measured candidate: `7c5a666ae11d84cd3e348418267f9781706c97c3`.
- Warm-prefix: 643342 to 684886 B/op (relative +6.4575%), 5585 to 5906 allocs/op (+5.7475%). Cold-prefix: 725409 to 766701 B/op (+5.6922%), 6152 to 6470 allocs/op (+5.1691%). Relative limits remain 5%; absolute allocation limits remain 10%. CPU-adjusted time and process peak-RSS limits pass.
- Five repeated measurements at pre-repair source `70117feaa45b85d045670219d598fe9d1f1428e8` reproduce the current allocation counts and byte totals. TASK 525 parent `d0e5a2a6d27c6689ba958620b0ff546b754a7e43` uses approximately 643804 B/op and 5594 allocs/op warm; TASK 525 commit `cb71a3b7a33bd651da95875632dfbdb422ef4aff` uses approximately 684857 B/op and 5906 allocs/op warm.
- Fixed-work allocation profiles across that boundary attribute approximately 3991 KiB additional cumulative allocation to PreDecisionDependencies, including approximately 3889 KiB in normalizeEvaluationInput, across the profile's repeated workload and setup. These are profile totals, not B/op. Additional path resolution and command normalization dominate the delta even in the empty-rule fixture.
- Full comparison: `.build/benchmarks/task538-comparison.json`; raw results and hashes are adjacent. Attribution logs and profiles: `.build/review-repairs/task538-before-525-*`, `task538-after-525-*`, `task538-before-repairs-session.log`, and `task538-allocation-cumulative-diff.txt`.

## Deviations

None.
