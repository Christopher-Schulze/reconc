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
- [x] Inspect the native paired result, retain residual diagnostics, archive, commit, and push.

## Notes

- Native CI 34597546372 and CodeQL 34597546362 pass for clean commit
  6116c64d8b6fe9f391177368ba851e450d09f8ed, including Linux, macOS, Windows,
  LangChain, and release trust. Whole-module coverage is 82.2690% root and
  84.1104% portable template.
- Paired run 34597552388 completes recording, profiles, reference-bound baseline
  generation, and artifact retention within 18 minutes. Its comparison is
  compatible but fails three limits; it is not a passing performance result.
  All 19 groups, 24 targets, and 30 artifact hashes/lengths were verified in
  `.build/benchmarks/task518-ci-34597552388/`. Parameters and reference tolerances
  match exactly, and the checked baseline remains byte-identical.
- Remaining limits: compiler package RSS 164,364,288 to 379,371,520 bytes
  (reported for both compiler targets); warm-prefix normalized time 4.465586
  to 5.941577 (+33.05%). All absolute target time, B/op, and allocs/op limits
  pass. Warm-prefix raw p50 rises 18.33% while p95 falls 4.61%; its calibrator
  falls 11.07%. This establishes a failed ratio, not its production cause.
- Direct local compiler binaries also show increased peak RSS: 77,217,792
  baseline versus 110,952,448 candidate bytes. The current Go driver peaks at
  158,187,520 bytes, so driver and workload peaks must not be conflated.
  Read-only conflict indexing copies full rule structs into two collections;
  TASK 519 will test removing those copies and resolve the residual diagnosis.
- The native warm/cold hook profile attributes 90.82% of sampled CPU to system
  calls, including required path-identity checks. Earlier controlled physical
  M1 samples had warm-prefix p50 -2.24% against the same baseline production
  code. Do not remove freshness checks or label virtual-runner variation as a
  proven cause. Retain both outcomes for TASK 519.
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
