# TASK 217: Make extracted rule identifiers collision-resistant

## Why

The prose extractor deduplicates suggestions by IDs derived from a lossy,
40-character slug. Different paths or commands can collapse punctuation or
share a truncated prefix and receive the same ID, causing the later valid
suggestion to disappear silently. Bare canonical filenames such as `Makefile`
also fail the current path plausibility heuristic.

## Acceptance

- Deduplication uses the semantic suggestion identity, including kind and
  normalized target/command, rather than the lossy display ID alone.
- Generated IDs remain readable, valid, deterministic, length-bounded, and add
  a stable digest suffix whenever slugging is not injective.
- Distinct suggestions never disappear because of punctuation folding or
  prefix truncation; true duplicates still collapse with combined provenance
  where appropriate.
- Plausible-path handling supports an explicit allowlist/rule for canonical bare
  repository filenames without accepting arbitrary noun phrases.
- Tests cover collisions, long Unicode inputs, repeated prose, bare filenames,
  ordering, and the 8 MiB CLI source bound.

## Sub-Tasks

- [x] Define semantic suggestion identity and deterministic ID generation
- [x] Implement collision-resistant IDs and provenance deduplication
- [x] Refine bare-filename plausibility explicitly
- [x] Add adversarial collision and compatibility tests
- [x] Run extractor, CLI, and complete gates

## Notes

- `Extract` now keys deduplication by kind plus normalized semantic targets:
  path lists for write/read rules, command lists for command rules, and claim
  lists for claim rules. True duplicates retain first output order and merge
  distinct evidence lines through a bounded-free, source-limited provenance
  list instead of silently discarding later citations.
- Generated IDs remain readable lower-kebab labels and are capped at 64 bytes.
  Any case, punctuation, whitespace, Unicode, or truncation loss adds the
  first ten hexadecimal characters of a SHA-256 digest over kind and the
  normalized target. This keeps punctuation-folding and long-prefix inputs
  distinct while preserving the fixed stable IDs for the built-in secret/CI
  suggestions.
- `Makefile`, `Dockerfile`, `AGENTS.md`, and other explicit canonical bare
  repository filenames are allowlisted; arbitrary bare nouns remain rejected.
  Collision, long-Unicode, ordering, duplicate-provenance, and command
  normalization tests pass, including the existing CLI 8 MiB source bound.

## Deviations

None.
