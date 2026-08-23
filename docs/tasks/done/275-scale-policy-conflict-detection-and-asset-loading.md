# TASK 275: Scale policy conflict detection and asset loading

## Why

Static duplicate-conflict detection compares every rule pair and allocates and
sorts both list fields inside the inner loop. At documented maximum rule/list
cardinality this performs repeated O(n log n) normalization on an O(n²) pair
space. Template expansion also resolves and reparses the same template for
every referencing rule. User preset manifest parsing bypasses the shared YAML
node/alias/depth budgets, and explicit YAML `null` is currently treated as an
empty mapping despite the decoder's mapping-only contract.

## Acceptance

- Each conflict-relevant rule field is trimmed, normalized, sorted, and keyed
  once. Rules are grouped by semantic key, then the exact deterministic pair
  set and descriptions currently emitted are generated from each group.
- Duplicate deny, require-read, require-command, require-claim, deny-vs-read,
  and forbid-vs-require behavior and output ordering remain byte-stable for the
  existing corpus.
- Conflict analysis stays bounded even when the output itself is quadratic;
  maximum conflict count/bytes are explicit or streaming publication prevents
  unbounded materialization.
- Template resolution uses a compile-scoped immutable cache keyed by normalized
  name plus exact source identity/content. User override precedence and
  mid-compile replacement detection remain fail closed.
- User preset parsing enters `yamlbound` before any `yaml.Unmarshal`/
  `yaml.Node.Decode` and enforces the same node, alias, depth, scalar, aggregate,
  and single-document limits as policy sources.
- The contract for empty/comment-only and explicit-null policy documents is
  decided explicitly. If null is rejected, migration/error text and tests make
  the compatibility change visible; it must not silently compile zero rules.
- Schema regexp execution has a finite timeout and propagates engine failure as
  validation failure rather than silently treating it as no-match. Shared
  adapters replace production/test copies only where behavior is identical.
- Parser bounds are walked once per expanded rule where possible, without
  dropping pre-expansion or post-template safety validation.
- Scaling benchmarks, alias-bomb tests, replacement races, deterministic golden
  tests, docs, schemas, and complete gates pass.

## Sub-Tasks

- [x] Capture deterministic conflict output and maximum-cardinality baselines
- [x] Precompute semantic rule keys and group duplicate candidates
- [x] Bound or stream quadratic conflict output while preserving exact order
- [x] Add a compile-scoped identity-checked template cache
- [x] Route preset manifests through shared bounded YAML admission
- [x] Resolve and document empty versus explicit-null policy semantics
- [x] Add finite regexp timeouts and fail-loud adapter errors
- [x] Remove only proven duplicate parser-bound walks
- [x] Add scaling, determinism, alias-bomb, replacement, and compatibility tests
- [x] Update policy authoring, limits, schema, and performance documentation
- [x] Run parser/compiler/schema/fuzz/race and complete repository gates

## Notes

- Current evidence: `internal/compiler/conflicts.go:findExactDuplicates` and
  `findDuplicateRequireReads` call `slicesEqualSorted` in nested pair loops;
  that helper allocates and sorts both inputs on every comparison.
- Current evidence: `internal/parser/parser.go:expandTemplate` calls
  `templates.Resolve` per rule; `internal/presets/loader.go:parseManifest` uses
  unbounded `yaml.Unmarshal` on already byte-bounded but structurally
  adversarial user content.
- Current evidence: `yamlbound.DecodeMapping` maps decoded `nil` to an empty map
  even for explicit `null`. Existing parity tests prove legacy behavior, not
  that the behavior is a desired public contract.
- Lockdiff's repeated envelope/index checks are cheap relative to correctness
  and should be changed only if profiling shows a material duplicate pass.
- `optionalInt` does not need an extra `uint64` arm unless an actual decoder path
  can produce that type. Do not add unreachable compatibility code.
- Duplicate candidates now use one length-delimited normalized semantic key per
  rule. Exact conflict descriptions and final ordering remain unchanged below
  the explicit 65,536-pair publication limit; overflow adds one deterministic
  `analysis_truncated` sentinel instead of materializing unbounded output.
- Template values are cached for one compilation and re-resolved in sorted name
  order before publication. Any disappearance or identity/content replacement
  fails the whole compilation.
- Empty and comment-only policy documents remain valid zero-rule inputs.
  Explicit YAML `null` is rejected with migration text. Preset manifests now
  pass through the same bounded YAML admission as policy inputs.
- The shared ECMAScript regexp adapter caps evaluation at 100 ms and fails
  closed on engine errors. The JSON Schema callback cannot return a distinct
  runtime error, so a timeout becomes a validation mismatch rather than being
  silently accepted.
- Security-motivated pre-expansion and post-template bound checks remain. No
  parser pass was removed without evidence that its postcondition was already
  guaranteed.
- Performance history advanced to `reconc.performance-history/v7`. On Apple M1,
  the 4,096-unique-rule calibration median was 3.73 ms/op and the 32,640-pair
  grouped case was 11.20 ms/op; output construction dominates the latter by
  design.
- Verification: focused compiler/parser/preset/schema race tests; three fuzz
  runs totaling 308,143 executions; `make test-fast`; `make vet`; `make lint`;
  benchmark record/baseline/compare; and `make publication-audit` all passed.

## Deviations

None.
