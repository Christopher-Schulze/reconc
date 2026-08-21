# TASK 194: Parse repository configuration once

## Why

`LoadPolicySources` reads `.reconc.yml` once but converts the bytes to strings
and unmarshals the same YAML separately for include patterns and preset
extensions. The two extractors can also drift in structural validation because
they do not share one decoded document.

## Acceptance

- One bounded strict YAML decode produces the authoritative configuration
  representation used for includes, extends, and every source-loading field.
- Duplicate-key, root-type, scalar/list, unknown-field, and source-location
  behavior is specified and consistent with the canonical config parser.
- Raw bytes are not copied repeatedly solely to feed independent decoders.
- Tests prove identical valid ordering and improve errors for malformed mixed
  include/extends configurations.
- Benchmarks show one YAML decode per source-load transaction.

## Sub-Tasks

- [x] Define the typed/intermediate repository config document
- [x] Decode and validate it once in source loading
- [x] Migrate include and preset extraction
- [x] Add malformed-config, compatibility, and benchmark tests
- [x] Run ingest, parser, compiler, and complete gates

## Notes

- `LoadPolicySourcesWithContext` decodes the config once into one mapping and
  routes both include and preset extraction through document-owned helpers;
  public text helpers remain compatibility wrappers for focused callers.
- Existing malformed YAML, scalar/list, path, and preset validation behavior
  remains unchanged. Benchmark on Apple M1: one decode 5.292 us/op,
  8,672 B/op, 81 allocs versus two decodes 10.341 us/op, 17,280 B/op,
  159 allocs.

## Deviations

None.
