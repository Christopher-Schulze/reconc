# TASK 518: Pair baseline and candidate benchmark samples

## Why

Run 34593702927 successfully records both exact clean sources on the same
runner, preserves all checked tolerances, and retains 30 verified profile
artifacts. Its comparison correctly reports six budget violations. However,
the baseline suite runs completely before the candidate, and the baseline is
started directly while the candidate inherits Make's additional environment.
The process-start benchmark measures that parent environment as well as its
child; sequential phases also retain avoidable scheduling and thermal bias.

## Acceptance

- Record both source trees from one tool process with the same inherited
  environment, alternating baseline/candidate order for successive samples.
- Preserve every workload, sample/repetition count, CPU sentinel, statistic,
  RSS high-water mark, and checked regression limit; do not refresh the source
  baseline or discard the failed comparison.
- Validate exact baseline commit, compatible clean source environments and
  unchanged source identities around recording; retain both results and profiles.
- Real Go subprocess tests prove paired execution order, complete samples and
  cancellation. Local gates and one native paired run establish the outcome;
  residual violations remain explicit and require diagnosis.

## Sub-Tasks

- [x] Inspect every failed metric, raw samples, launcher paths, and source differences.
- [x] Add paired recording to the existing benchmark tool and use it in CI.
- [x] Verify real execution order and failure boundaries, flush documentation and run gates.
- [~] Inspect the native paired result, finalize dependent TASKs, commit, and push.

## Notes

- Complete `make test`, `make vet`, and `make lint` pass, including root and
  portable-template race suites, publication/reference checks, and real isolated
  release-trust verification (90-second release target). The checked baseline's
  SHA-256 remains dbf4c975b819b539447376cb0906c4c99bf93093d0540ec6965b2702cb1b4dde.
- Focused benchmark/publication tests pass. Real Go subprocesses record
  baseline/candidate/candidate/baseline order with complete samples and CPU
  sentinels; canceled and missing workloads cannot emit measurement logs.
- Environment checks reject wrong baseline commits, dirty or mutable sources,
  changed toolchains/CPUs/parameters, and existing or aliased output paths.
  `record-pair` uses the checked sample parameters and preserves raw statistic
  semantics. Package pairing has a ten-minute total bound for both sources.
- Failed evidence: `.build/benchmarks/task514-ci-34593702927/`. Both environments
  are Go 1.27.1/darwin/arm64/Apple M1 (Virtual); baseline c814f5b40579d2feed47231ec9dc2d80afda1dea,
  candidate bcceb9d085ead6e74b5e0b2be232801b914ff241. Reference tolerances are
  byte-equivalent after decoding; all 30 profile hashes and lengths match.
- Absolute timing limits all pass. Action target time falls 5.45% while its
  calibrator falls 21.27%, producing a 20.09% ratio violation; action source is
  unchanged. One-shot time falls 31.76% while worker time falls 45.49%, producing
  a 25.17% ratio violation. Its parent-process B/op rises 17,608 to 21,320.
- Compiler package process RSS rises 334,495,744 to 555,646,976 bytes. Both
  compiler groups share the same Go test process, so these are two reported
  limits on one package high-water mark, not isolated per-function heap proof.
- Preserve the recorded failures. Paired sampling addresses launcher/order
  differences without declaring every observed difference a production defect
  or claiming that a subsequent result erases the earlier run.

## Deviations

- Native proof requires a locally verified candidate commit on main under the
  standing push instruction. Keep the TASK active until that proof completes.
