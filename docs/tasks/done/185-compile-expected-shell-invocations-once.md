# TASK 185: Compile expected shell invocations once

## Why

Forbidden-command evaluation parses each expected command with the shell
syntax parser inside nested observed-command and expected-command loops.
Expected commands are immutable policy data, but their invocation trees are
rebuilt for every comparison.

## Acceptance

- Expected shell commands are parsed once into immutable bounded invocation
  state during plan construction or a single evaluation preparation phase.
- Parse completeness, uncertainty, depth and invocation caps, redirections,
  pipelines, subshells, and fail-closed outcomes remain identical.
- Observed command parsing is performed at most once per distinct input command
  per evaluation.
- Table-driven differential tests cover complex shell forms and malformed
  syntax; benchmarks cover nested rule/command cardinalities.
- No parser object or mutable syntax tree is shared unsafely across evaluations.

## Sub-Tasks

- [x] Specify compiled expected-command representation and bounds
- [x] Precompute expected invocations at the correct trust boundary
- [x] Reuse observed-command parses within one evaluation
- [x] Add differential, fuzz, race, and benchmark tests
- [x] Run shell-command, runtime, and complete gates

## Notes

- Evidence: `shellcommand.Invocations` in
  `internal/shellcommand/shellcommand.go` and
  `matchingForbiddenCommands` in `internal/runtime/evaluator.go`.
- `shellcommand.CompiledExpectation` owns one immutable bounded invocation
  parse and exposes the same match/uncertainty decision as the existing
  helpers. The public helpers remain compatible wrappers that compile one
  temporary expectation for dynamic callers.
- Runtime evaluation builds a per-evaluation `commandInvocationCache` after
  command normalization. It compiles every policy and composite expected
  command once, then caches observed invocation extraction by exact command
  text. The cache is context-owned, never shared between evaluations, and
  preserves fail-closed incomplete/dynamic handling.
- The cache is used by top-level forbid-command checks, composite forbid checks,
  and matched-rule tracing. Literal require-command evidence continues to use
  its existing normalized comparison path because it does not parse shell
  syntax.
- Compatibility tests cover compound commands, wrappers, nested shells,
  dynamic words, malformed syntax, depth and size limits, prefix mode, and
  executable case folding. The representative benchmark measured roughly
  4.3 microseconds/38 allocations with prepared state versus 66 microseconds/
  1153 allocations when expected and observed syntax was reparsed.
- Verification: `go test ./internal/shellcommand ./internal/runtime`, race
  tests for both packages, and full `make test` passed, including publication
  audit, harness-pack validation, all repository race tests, template races,
  and release trust.

## Deviations

None.
