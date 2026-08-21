# TASK 229: Make command normalization idempotent

## Why

`normalizeCommandSemantics` documents an idempotent transformation, but each
command segment removes at most one leading `rtk` proxy token. A stacked input
such as `rtk rtk git status` therefore normalizes to `rtk git status` on the
first pass and `git status` on the second. Evidence indexes and policy-side
expectations must converge to one identity in one pass for every accepted input
rather than only for the current test seeds.

## Acceptance

- Every command string satisfies `normalize(x) == normalize(normalize(x))` for
  any repository root, including repeated wrapper prefixes in every compound
  command position.
- Wrapper removal consumes only complete unquoted leading `rtk` tokens; it does
  not alter `rtkfoo`, quoted literals, escaped data, argument values, or text
  inside shell substitutions.
- Each normalization step strictly shortens or advances the input, has an
  explicit bound, and cannot loop on malformed or adversarial text.
- Existing compound separator, line continuation, whitespace, absolute-root
  `cd`, quoted path, command-match, command-result, and assurance behavior is
  byte-compatible.
- Policy expectations and recorded evidence use the same normalized identity
  without a second compensating pass or duplicate cache entry.
- Table tests include stacked prefixes at command start and after every
  supported separator; property/fuzz tests assert idempotence, determinism,
  boundedness, and preservation of quoted shell data.
- Runtime benchmarks show no material regression for ordinary single-wrapper
  and wrapper-free commands.

## Sub-Tasks

- [x] Define the exact repeated-wrapper normalization grammar
- [x] Make per-segment normalization converge in one bounded pass
- [x] Extend command, evidence-index, and policy-match regressions
- [x] Add idempotence and quoted-data fuzz properties
- [x] Run runtime race, benchmark, and portable command gates

## Notes

- Session finding: `#8`.
- Primary code: `internal/runtime/evaluator.go`,
  `internal/runtime/command_wrapper_compat_test.go`, and runtime evaluator
  tests.
- Existing behavior intentionally strips `rtk` even for an `rtk` binary
  subcommand such as `rtk hook claude`; changing that policy is outside scope.
- This TASK does not introduce shell execution or attempt a complete POSIX
  shell parser. It preserves the existing bounded segment scanner.
- Each command segment removes up to `len(segment)/len("rtk ")` complete
  leading wrapper tokens. Every iteration strictly shortens the segment and a
  second normalization pass is byte-identical.
- The segment scanner now tracks nested unquoted `$(...)` parentheses, so
  compound separators inside command substitution remain data for this
  normalizer. Quotes, backticks, escapes, argument values, and malformed
  unterminated substitutions remain unchanged.
- Evidence indexes and policy expectations collapse literal, single-wrapper,
  and stacked-wrapper forms to one cached identity; command-result matching
  proves the same parity in both directions.
- Verification: complete runtime tests, focused race tests, `go vet`, and a
  three-second fuzz campaign pass. The fuzz campaign executed 9,005 cases with
  no idempotence, determinism, or quoted-data failure. Apple M1 benchmark
  medians were approximately 2.8 us/208 B/6 allocs for wrapper-free commands,
  4.1 us/224 B/6 allocs for one wrapper, and 8.1 us/340 B/8 allocs for five
  wrappers across two compound segments.

## Deviations

None.
