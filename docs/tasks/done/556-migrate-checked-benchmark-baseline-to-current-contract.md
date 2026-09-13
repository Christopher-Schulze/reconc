# TASK 556: Migrate checked benchmark baseline to current contract

## Why

The checked `scripts/benchmarks/baseline.json` uses benchmark baseline/result v4, while the current recorder emits v6. The documented default `make benchmark-compare` therefore cannot compare a new result against the checked reference. TASK 554 used an ignored v6 reference for its paired measurements and deliberately did not refresh the checked baseline.

## Acceptance

- The checked reference uses the current v6 baseline/result contract and covers the complete current target inventory.
- Its source commit, environment, parameters, tolerances, and measurement provenance are explicit; no old v4 measurements are relabeled as v6.
- A clean new record compared through the documented default `make benchmark-compare` completes with a compatible report, without weakening tolerances or omitting targets.
- Existing legacy-format validation and regression tests remain intact; documentation states what the checked baseline represents.

## Sub-Tasks

- [x] Inventory the checked v4 reference, current v6 contract, full target set, and retained TASK 554 measurements.
- [x] Select and record a clean source-bound v6 reference with the existing tolerances; preserve the old reference as historical evidence before any refresh.
- [x] Refresh the checked baseline through the guarded benchmark workflow and verify target/parameter/environment identity.
- [x] Run a full current record and the default comparison; update documentation and complete relevant tests.

## Notes

Discovered during TASK 555. `Makefile` defaults `BENCHMARK_BASELINE` to `scripts/benchmarks/baseline.json`; at discovery that file declared `reconc.benchmark-baseline/v4` and `reconc.benchmark-result/v4`, while `scripts/benchmarks/history/contract.go` declared current v6 formats. TASK 554 recorded a compatible ignored v6 reference and complete paired measurements, but its report explicitly left the checked baseline unchanged. This was separate benchmark maintenance, not evidence that TASK 554's measured optimization failed.

The old checked v4 reference bound clean commit `c814f5b40579d2feed47231ec9dc2d80afda1dea`, Go 1.27.1, darwin/arm64 Apple M1, five samples, three repetitions, 250 ms, 19 groups, and 24 targets. Its seven tolerance values were 0.20 normalized time, 0.05 normalized bytes/allocations, 0.20 absolute time, 0.10 absolute bytes/allocations, and 0.20 absolute peak RSS. Its original SHA-256 was `dbf4c975b819b539447376cb0906c4c99bf93093d0540ec6965b2702cb1b4dde`; `git mv` preserved that exact content at `scripts/benchmarks/baseline-v4.json`. The ignored TASK 554 v6 artifact was removed by the standard release-trust gate, so it was not silently reused.

Two independent `make benchmark-record` runs measured the clean pushed commit `13bbfedb040b33d0ead5fd9ef3d6a7141911978c` with Go 1.27.1 on darwin/arm64 Apple M1, five samples, three repetitions, and 250 ms per sample. Both results declared `dirty=false`, result v6, suite v10, 19 groups, and all 24 targets. The reference result SHA-256 was `1efa30c81074ff6c05dcc696d84bd56551dc6ec5f55a712211666d450a0b81aa`; the second result SHA-256 was `258a585dee97b41fd05814132db31428a59b8165198aa97042b9043d74a8353d`. The second result was copied byte-for-byte to the documented default `.build/benchmarks/current.json` before comparison.

With the old file moved aside, `make benchmark-baseline BENCHMARK_RESULT=.build/benchmarks/task556-reference.json CONFIRM_BENCHMARK_BASELINE=1` created the new checked baseline, SHA-256 `b59adea7ab214732bcca8cc8863dd1647cf1afb9915f479b0b7168a339c50dbb`. `baseline-commit` returned the exact reference commit; machine, parameters, inventory, and every tolerance were checked against the source result and historical v4 tolerances. The unchanged default `make benchmark-compare` wrote comparison v8, SHA-256 `1a1c95d3fa5b4c4ecdbddc4d0a32d1aadb15470fba79a95d50cfbf088e6114d6`, with `compatible=true`, `passed=true`, all 24 target groups, and no regressions. Ignored `.build` measurements may be removed by `make test`; the checked v6 baseline and this source-bound record are durable.

The new `checked_baseline_test.go` reads the real checked baseline, requires the current v6 result/baseline and suite contracts, and compares every group, calibration, package, and target with the current suite. It also reads the historical file under its genuine v4 contract. The focused test, `make test` (root and portable uncached race suites, publication audit, reference docs, harness pack, release trust), `make vet`, `make lint`, `make build`, and `git diff --check` passed on the final inputs. Release trust removed the ignored measurement files as expected; the checked v6 and v4 baseline SHA-256 digests remained unchanged afterward.

## Deviations

None.
