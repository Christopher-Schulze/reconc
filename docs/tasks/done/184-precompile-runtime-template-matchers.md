# TASK 184: Precompile runtime template matchers

## Why

Template-aware path matching calls `compileTemplatePattern` during evaluation.
Write paths are compared against the same rule patterns repeatedly while match
contexts are collected, so regex construction and masked/bound pattern setup
are repeated for immutable rule data.

## Acceptance

- Every template pattern is parsed and compiled once when the runtime plan is
  constructed, with explicit literal, masked, and bound matching state.
- Evaluation performs no regex compilation for a valid immutable plan.
- Variable capture order, repeated-variable equality, first-pattern semantics,
  separator behavior, and invalid-template failures remain byte-for-byte
  compatible at the public report boundary.
- Differential and fuzz tests compare old and compiled matcher behavior across
  malformed and adversarial patterns.
- Benchmarks cover writes multiplied by patterns and prove the hot-path gain.

## Sub-Tasks

- [x] Define immutable compiled template matcher state
- [x] Compile and validate matchers during plan construction
- [x] Route all template match paths through compiled state
- [x] Add differential, fuzz, and benchmark coverage
- [x] Run runtime and complete Go gates

## Notes

- Verified in `internal/runtime/template.go`, especially `MatchTemplate` and
  `compileTemplatePattern`.
- `compiledTemplateMatcher` stores the original pattern, literal or masked
  compiled glob, deferred validation errors, capture regex and ordered names,
  plus pre-parsed bound-substitution parts. Deferred errors preserve the old
  ordering: malformed masked globs fail before regex work, duplicate capture
  names fail only after a masked candidate hit, and non-matching candidates do
  not surface latent template errors.
- `runtimePlan.templateMatchers` is built once for every context-bearing
  `when_paths` rule (`require_fresh_file`, `require_evidence`,
  `require_script`, `all_of`, `any_of`, `not`). Evaluation, batched scripts,
  composite setup, matched-rule tracing, and metrics use the plan-owned map;
  dynamic public helpers retain their previous isolated behavior.
- Differential tests compare compiled and legacy behavior for literals,
  globstars, alternatives, character classes, captures containing glob
  metacharacters, malformed globs, duplicate variables, and misses. The fuzz
  suite remains active. The local Apple M1 benchmark measured roughly
  0.9 microseconds/7 allocations for compiled matching versus 7.4
  microseconds/118 allocations for compiling on every call.
- Verification: `go test ./internal/runtime`, `go test -race ./internal/runtime`,
  and full `make test` passed, including publication audit, harness-pack,
  every race-enabled package, harness template races, and release trust.

## Deviations

None.
