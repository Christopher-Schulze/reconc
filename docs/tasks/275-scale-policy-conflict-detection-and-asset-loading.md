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

- [ ] Capture deterministic conflict output and maximum-cardinality baselines
- [ ] Precompute semantic rule keys and group duplicate candidates
- [ ] Bound or stream quadratic conflict output while preserving exact order
- [ ] Add a compile-scoped identity-checked template cache
- [ ] Route preset manifests through shared bounded YAML admission
- [ ] Resolve and document empty versus explicit-null policy semantics
- [ ] Add finite regexp timeouts and fail-loud adapter errors
- [ ] Remove only proven duplicate parser-bound walks
- [ ] Add scaling, determinism, alias-bomb, replacement, and compatibility tests
- [ ] Update policy authoring, limits, schema, and performance documentation
- [ ] Run parser/compiler/schema/fuzz/race and complete repository gates

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

## Deviations

None.
