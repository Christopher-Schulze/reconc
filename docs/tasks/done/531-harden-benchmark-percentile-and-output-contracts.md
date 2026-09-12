# TASK 531: Harden benchmark percentile and output contracts

## Why

The PGO report labels one cold-start statistic `p95_ns`, but computes it with
`int(0.95 * (n - 1))`. With the fixed seven-sample set this selects the
second-largest observation rather than a defined nearest-rank 95th percentile.
Separately, the non-recipe benchmark comparison reads its baseline and result
before publishing `--output`, but does not reject an output path that aliases
either input. A caller can therefore replace the reviewed baseline or raw result
with a comparison report.

This task consolidates the valid portions of audit candidates C243 and C244.
Shared benchmark-process RSS, platform-specific `Maxrss` conversion, and strict
zero-baseline behavior are already intentional or documented and remain out of
scope.

## Acceptance

- Define the P95 estimator explicitly. If retaining the `p95_ns` field, use the
  nearest-rank definition `ceil(0.95 * n) - 1`, bounded to the sorted sample
  range; for seven samples the selected value is the maximum.
- Define behavior for one, two, seven, and arbitrary positive sample counts,
  duplicate values, unsorted input, and invalid empty input.
- Keep raw `samples_ns`, median, minimum, maximum, and range exact and derived
  from the same captured samples; do not alter cold-start execution count or
  hide variability.
- Inspect every persisted PGO consumer. If corrected percentile semantics can
  be compared with historical reports, version the measurement contract and
  reject mixed semantics rather than silently relabel old data.
- Before any comparison publication, prove `--output` is distinct from both
  `--baseline` and `--result` after absolute/canonical normalization.
- Reject direct path equality, relative/absolute aliases, cleaned-path aliases,
  symlink aliases, and hardlink/same-file aliases. Apply the same protection to
  recipe and non-recipe comparison through one shared preflight.
- Preserve both input files byte-for-byte and metadata-for-metadata on every
  alias or publication error. Perform all validation before opening or replacing
  the output target.
- Preserve atomic output publication, stdout behavior when `--output` is
  absent, deterministic JSON, output bounds, comparison exit semantics, and
  input validation.
- Preserve every checked benchmark tolerance, baseline source identity,
  hardware/toolchain compatibility rule, sample/repetition count, execution
  order, CPU sentinel, fixed-work RSS method, and retained historical artifact.
- Add table-driven statistic tests and CLI/file tests for distinct outputs,
  every alias form, existing destinations, missing parents, encoding failures,
  comparison violations, recipe mode, and stdout mode.
- Add a failure-injection regression proving no input can be truncated even if
  comparison encoding or atomic publication fails after inputs are read.
- Update benchmark documentation with the exact percentile estimator and output
  non-aliasing contract.
- Pass benchmark-history tests under the race detector, Bash and embedded Python
  syntax checks, real subprocess tests, publication/reference audits, and all
  required repository gates.

## Sub-Tasks

- [x] Read the complete PGO sampler/report/consumer path, benchmark artifact versions, comparison CLI modes, output preflights, atomic publisher, path-identity helpers, and all benchmark tests.
- [x] Specify and centralize the percentile estimator with exact small-sample semantics and determine whether persisted contract versioning is required.
- [x] Correct P95 generation and cover raw/derived statistic consistency without changing sample collection.
- [x] Reuse or extend one canonical output-alias preflight for recipe and non-recipe comparison before any write-capable operation.
- [x] Add statistic boundary, path alias, same-file, symlink, hardlink, failure-injection, input-preservation, stdout, and comparison-error regressions.
- [x] Verify checked baselines, tolerances, historical readers, RSS semantics, result formats, and retained artifacts remain unchanged unless explicit semantic versioning requires a new PGO format.
- [x] Update benchmark method and CLI publication documentation.
- [x] Run focused race, shell/Python, real subprocess, publication, and complete repository gates; re-read every modified file before archival.

## Notes

### Observed behavior

- PGO `p95_ns` used `int(0.95 * (n - 1))`, selecting the second-largest of seven
  samples. Non-recipe compare published `--output` without an alias preflight.

### Implementation

- `pgo_stats.p95_ns` uses nearest-rank `ceil(0.95 * n) - 1`. Report format is
  `reconc.benchmark-pgo/v2` so v1 values are not relabeled.
- `rejectAliasedComparisonOutput` is shared by recipe and non-recipe compare
  and runs before any output write. Inputs stay byte-and-metadata intact on
  alias, encode, and publication failure.

### Verification

- `pgo_stats.py` self-test plus `py_compile`. Alias tests cover string, relative,
  cleaned, symlink, hardlink, recipe, existing destination, encode/publish
  failure, and input preservation. `bash -n scripts/benchmarks/pgo.sh`,
  `go test -race ./scripts/benchmarks/history`, `make lint`, and `make test`
  passed.

### Contract invariants

- A statistic name has one mathematical meaning per artifact version.
- Raw samples remain the audit trail for every derived statistic.
- Historical values are never relabeled as if computed by a new estimator.
- Baseline and result files are immutable inputs to comparison.
- Output publication never targets the same filesystem object as an input.
- A failed comparison may still publish a valid report to a distinct output,
  preserving the current exit contract.
- Existing performance limits and measurement methods do not change as a side
  effect of this correctness task.

### Verification matrix

- Sample counts one, two, three, seven, and larger odd/even sets.
- Strictly increasing, equal, repeated, and extreme integer samples.
- Baseline/result/output as identical strings, relative/absolute aliases,
  normalized aliases, symlinks, hardlinks, distinct files, and stdout.
- Passing and failing comparisons; recipe and non-recipe modes; existing and
  absent output; encoding and atomic-publication failure.
- Current and historical PGO/report formats according to the final versioning
  decision.

## Deviations

None.
