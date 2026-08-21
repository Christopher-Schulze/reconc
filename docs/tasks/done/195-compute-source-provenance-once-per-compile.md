# TASK 195: Compute source provenance once per compile

## Why

Compiler rendering converts each policy source to a provenance map and hashes
its string content for the aggregate source digest, then repeats the conversion
and hash while building the lock payload. Large source sets therefore perform
duplicate full-content copies and SHA-256 work inside one immutable compile.

## Acceptance

- Each source's canonical provenance record and content digest are computed
  exactly once after the source bundle is frozen.
- Aggregate source digest and serialized lock payload consume the same immutable
  records, eliminating any possibility of divergent representations.
- Source order, kind, path, block identity, content digest format, and lockfile
  bytes remain compatible unless an explicit format migration is justified.
- Tests compare output bytes/digests with existing golden fixtures and detect
  mutation after provenance construction.
- Benchmarks cover large sources and source counts and prove reduced hashing and
  allocation work.

## Sub-Tasks

- [x] Define immutable compiled source provenance records
- [x] Compute them once at the render boundary
- [x] Thread records into digest and lock-payload construction
- [x] Add compatibility, mutation, and benchmark tests
- [x] Run compiler, runtime-lockfile, and complete gates

## Notes

- `sourceProvenance` now owns one ordered record slice and its aggregate
  digest. `renderPolicyBundle` computes it once; `buildLockPayload` reuses the
  records instead of reconstructing source maps and hashes.
- Golden compile/source-digest behavior remains byte-compatible. Mutation tests
  prove records are frozen independently of later source-body changes.
- Apple M1 benchmark with 256 sources: prepared record reuse 139.523 us/op,
  99,585 B/op, 1,805 allocs versus duplicate conversion/digest work 435.050
  us/op, 520,461 B/op, 7,710 allocs.

## Deviations

None.
