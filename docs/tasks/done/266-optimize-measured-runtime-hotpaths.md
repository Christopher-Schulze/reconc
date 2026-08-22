# TASK 266: Optimize measured runtime hotpaths

## Why

Fresh Go 1.27 benchmarks and allocation profiles identify avoidable work in
bounded action traces, evaluation-local evidence memos, runtime freshness
hashing, detector-pack construction, clean structured scans, immutable value
traversal, and repository-source loading. These paths should retain their
existing deterministic, privacy, filesystem-identity, and fail-closed contracts
while reducing allocation volume and repeated computation.

## Acceptance

- Action evaluation retains at most the published trace count and byte budgets
  while rules are evaluated, reports the exact omitted count, and never builds
  the discarded tail in memory.
- Evaluation-local evidence caches allocate storage in proportion to observed
  entries rather than their maximum cardinality, without changing cache bounds,
  eviction order, cloning, or error semantics.
- Runtime freshness reuses one bounded hash-copy buffer per source batch and
  preserves file size, identity, mutation, symlink, and aggregate-byte checks.
- MCP gateway inspections reuse one immutable, concurrency-safe compiled
  detector pack across request phases; clean scans and binary-free value walks
  avoid per-value findings and pointer allocations.
- Immutable action values expose bounded read-only indexed traversal, and hot
  structured walkers use it without exposing mutable backing slices or changing
  JSON, schema, predicate, privacy, or result behavior.
- Policy source loading uses one Go 1.27 rooted filesystem handle per load,
  rejects root escape, retains stable-file snapshot checks, detects root and
  source replacement, closes every handle, and remains portable across all
  supported platforms.
- Focused unit, adversarial filesystem, concurrency, race, fuzz-compatible,
  benchmark-history, documentation, script, and repository-wide gates pass.
  Before/after benchmark evidence records allocation and latency changes without
  claiming noisy timing as a universal speedup.
- The completed task is archived and committed locally on `main`; no branch,
  push, tag, release, or version change is created.

## Sub-Tasks

- [x] Capture benchmark baselines and map all affected contracts
- [x] Bound trace collection and right-size evaluation-local memos
- [x] Reuse runtime freshness hashing storage
- [x] Reuse detector compilation and remove clean-scan allocation churn
- [x] Add immutable indexed value traversal and migrate hot walkers
- [x] Introduce the rooted batch source reader with adversarial coverage
- [x] Update benchmarks, scripts, documentation, and task evidence
- [x] Run focused, race, static, publication, and complete verification
- [x] Archive TASK 266 and commit the verified change locally on main

## Notes

- The pre-change calibrated measurements on Go 1.27.0, Darwin/arm64, Apple M1
  include 8.410 ms, 407,928 B/op, and 4,015 allocs/op for contextual policy
  source ingestion; 2.803 ms, 267,832 B/op, and 2,087 allocs/op for batched write
  epochs; and 20.421 us, 1,838 B/op, and 38 allocs/op for canonical JSON.
- Focused pre-change measurements include 1.28-1.68 ms, 3,184,776 B/op, and
  841 allocs/op for a maximum legal action plan; 23-42 ms, 4,846,336 B/op, and
  32,777 allocs/op for a maximum legal MCP content array; and 16-18 ms,
  approximately 5.39 MB/op, and approximately 5,230 allocs/op for a large
  runtime-freshness source set.
- Allocation profiles attribute the largest avoidable sources to discarded
  action trace storage, maximum-capacity memo order slices, one `io.Copy`
  staging buffer per freshness file, repeated detector regular-expression
  compilation, empty findings slices, cloned value child slices, and repeated
  filesystem path resolution.
- `os.Root` is available in the repository's Go 1.27 toolchain. It confines
  relative operations beneath one opened directory handle on Darwin, Linux,
  and Windows, including safe in-root symlink following. Stable opened-file and
  post-read path identities plus final context validation remain required.
- The final calibrated medians are 1.151 ms, 326,699 B/op, and 809 allocs/op
  for the maximum legal action plan; 20.732 ms, 178,336 B/op, and 88 allocs/op
  for the maximum legal structured-content scan; 13.001 ms, 757,837 B/op, and
  4,272 allocs/op for the large freshness set; and 3.338 ms, 103,640 B/op, and
  1,027 allocs/op for contextual source loading. The stable claims are the
  allocation reductions; local filesystem and CPU timing remains noisy.
- Relative to the recorded pre-change runs, retained bytes fell about 89.7%
  for the maximum action plan, 96.3% for the clean maximum MCP scan, 86% for
  the large freshness set, and 74.6% for contextual source loading. The MCP
  gateway audit benchmark also fell from about 3.3 MiB/op and 14,868 allocs/op
  to about 1.45 MiB/op and 4,650 allocs/op by compiling one detector pack per
  gateway instead of once per inspection phase.
- Go 1.27 may split one measured benchmark line across multiple `go test
  -json` output events. Two pre-fix recordings independently lost different
  metrics even though the package process succeeded. The bounded history
  parser now reconstructs the output stream before line parsing, has an exact
  fragmented-event regression test, and the suite contract is version 2 with
  nine calibrated groups.
- Final verification passed: focused package tests; Darwin race tests for both
  modules through `make test`; Windows/amd64 and Linux/amd64 cross-compilation
  of all affected packages; all 61 registered fuzz targets; formatting,
  generated-reference, Tidy, Vet, Staticcheck, and bounded NilAway checks;
  Govulncheck v1.7.0 for both modules with no vulnerabilities; publication and
  harness-pack audits; release-trust tamper cases; self-hosting; calibrated
  benchmark comparison; and the hash-pinned Python 3.13.14 LangChain boundary.
  Coverage is 82.6951% for the root module and 84.0628% for the template.
- The first local LangChain command used the machine's default Python 3.14 and
  correctly rejected its missing pinned packages. The same proof then passed
  in a disposable Python 3.13.14 environment installed from the hash-pinned
  lock; the environment was removed.

## Deviations

None.
