# TASK 556: Migrate checked benchmark baseline to current contract

## Why

The checked `scripts/benchmarks/baseline.json` uses benchmark baseline/result v4, while the current recorder emits v6. The documented default `make benchmark-compare` therefore cannot compare a new result against the checked reference. TASK 554 used an ignored v6 reference for its paired measurements and deliberately did not refresh the checked baseline.

## Acceptance

- The checked reference uses the current v6 baseline/result contract and covers the complete current target inventory.
- Its source commit, environment, parameters, tolerances, and measurement provenance are explicit; no old v4 measurements are relabeled as v6.
- A clean new record compared through the documented default `make benchmark-compare` completes with a compatible report, without weakening tolerances or omitting targets.
- Existing legacy-format validation and regression tests remain intact; documentation states what the checked baseline represents.

## Sub-Tasks

- [ ] Inventory the checked v4 reference, current v6 contract, full target set, and retained TASK 554 measurements.
- [ ] Select and record a clean source-bound v6 reference with the existing tolerances; preserve the old reference as historical evidence before any refresh.
- [ ] Refresh the checked baseline through the guarded benchmark workflow and verify target/parameter/environment identity.
- [ ] Run a full current record and the default comparison; update documentation and complete relevant tests.

## Notes

Discovered during TASK 555. `Makefile` defaults `BENCHMARK_BASELINE` to `scripts/benchmarks/baseline.json`; that file declares `reconc.benchmark-baseline/v4` and `reconc.benchmark-result/v4`, while `scripts/benchmarks/history/contract.go` declares current v6 formats. TASK 554 records a compatible ignored v6 reference and complete paired measurements, but its report explicitly leaves the checked baseline unchanged. This is separate benchmark maintenance, not evidence that TASK 554's measured optimization failed.

## Deviations

None.
