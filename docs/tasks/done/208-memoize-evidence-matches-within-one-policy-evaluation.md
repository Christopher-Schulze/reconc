# TASK 208: Memoize evidence matches within one policy evaluation

## Why

After a stable evidence file is loaded, multiple rules can still repeat the
same derived operations: text conversion, literal/regex checks, template
substitution results, and match-context construction. File-read caching alone
does not eliminate duplicate logical matching work.

## Acceptance

- An evaluation-scoped memo key includes stable file identity/content,
  normalized matcher identity, substituted bindings, and every option that
  affects the result.
- Cached results preserve matched context, reason, provenance, error, and
  deterministic ordering for each logical consumer.
- Regex or matcher objects are immutable and bounded; cache cardinality is
  derived from existing rule/evidence limits.
- Tests cover identical and near-identical matchers, different bindings,
  negative results, invalid patterns, and file identity changes.
- Benchmarks prove reduced match operations for shared evidence without slowing
  unique-match workloads materially.

## Sub-Tasks

- [x] Inventory derived evidence operations and semantic inputs
- [x] Define bounded memo keys and immutable results
- [x] Route simple and composite evidence matching through the memo
- [x] Add differential, identity, and benchmark tests
- [ ] Run runtime, race, and complete gates

## Notes

- This TASK complements TASK 187: TASK 187 owns stable physical reads; this
  TASK owns repeated derived matching on those bytes.
- The runtime now keeps a bounded evaluation-local logical-match memo keyed by
  file identity, size/mode/mtime, content digest, substituted file binding, and
  all evidence options. It preserves ordered reasons for top-level and
  composite consumers, while physical snapshot hits still revalidate identity.
- Template match contexts use a separate bounded memo keyed by length-prefixed
  write/pattern digests and return cloned capture maps. Invalid matcher errors
  are memoized deterministically; no cache crosses an evaluation boundary.
- Focused tests cover identical/near-identical options, negative results,
  changed content, mutable-capture isolation, invalid matcher reuse, and a
  shared-match benchmark. Full runtime/race gates remain for queue completion.

## Deviations

None.
