# TASK 378: Stream source-freshness identity construction

## Why

A cold runtime-plan load observes and hashes every policy source twice around the lockfile read. Each observation materializes identity strings and snapshot structs for up to 4,096 files only to serialize and hash them.

## Acceptance

- Cold-load freshness verification preserves the existing source-change fail-closed guarantee.
- Identity hashing streams canonical fields without building a second full JSON object graph.
- Any reduced second pass still detects content replacement, same-metadata swaps, additions, removals, and policy-root changes.
- Benchmarks prove lower cold-load allocations and bytes read; deterministic mutation hooks cover every TOCTOU window.

## Sub-Tasks

- [x] Record cold-load allocation, byte-read, and large-source baselines.
- [x] Design a streamed identity and minimal safe revalidation protocol.
- [x] Add deterministic source-mutation race tests.
- [x] Run focused freshness tests and benchmarks.

## Notes

- Verified from findings 9 and 18.
- `loadRuntimePlanOwned` performs two complete source observations; `observeFreshnessFile` builds identity strings and copied snapshots before hashing.
- Metadata-only revalidation is insufficient because same-size, same-mtime object replacement must remain detectable.
- Apple M1 baseline at `d252a485`, 20 iterations: one-source cold load 243,624 B/op, 1,483 allocs/op, and 40 freshness bytes/op; 128-source cold load 2,994,002 B/op, 18,758 allocs/op, and 2,648 freshness bytes/op.
- The first freshness identity now reuses content digests from the identity-bound source bundle, validates its source sets and repository object, and streams every canonical field directly into SHA-256. The independent publication observation still reads and hashes the complete current source set.
- Deterministic load-stage hooks cover mutation after the loaded source snapshot, after the seeded identity, and after lock revalidation. Adversarial tests replace same-size content with restored metadata at every boundary and cover additions, removals, new configs, new custom runtimes, and repository-root replacement.
- Apple M1 result at 20 iterations: one-source cold-load allocation bytes fell from 243,624 to a 242,352 B/op median and freshness reads fell from 40 to 20 bytes/op. The 128-source cold-load median fell from 2,994,002 to 2,352,788 B/op, from 18,758 to 18,390 allocs/op, and from 2,648 to 1,324 freshness bytes/op.
- Verified with uncached runtime and ingest package tests, the focused cold-load benchmark suite, `make test-fast`, and `git diff --check`.

## Deviations
