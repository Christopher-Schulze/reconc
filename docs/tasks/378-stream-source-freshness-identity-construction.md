# TASK 378: Stream source-freshness identity construction

## Why

A cold runtime-plan load observes and hashes every policy source twice around the lockfile read. Each observation materializes identity strings and snapshot structs for up to 4,096 files only to serialize and hash them.

## Acceptance

- Cold-load freshness verification preserves the existing source-change fail-closed guarantee.
- Identity hashing streams canonical fields without building a second full JSON object graph.
- Any reduced second pass still detects content replacement, same-metadata swaps, additions, removals, and policy-root changes.
- Benchmarks prove lower cold-load allocations and bytes read; deterministic mutation hooks cover every TOCTOU window.

## Sub-Tasks

- [ ] Record cold-load allocation, byte-read, and large-source baselines.
- [ ] Design a streamed identity and minimal safe revalidation protocol.
- [ ] Add deterministic source-mutation race tests.
- [ ] Run focused freshness tests and benchmarks.

## Notes

- Verified from findings 9 and 18.
- `loadRuntimePlanOwned` performs two complete source observations; `observeFreshnessFile` builds identity strings and copied snapshots before hashing.
- Metadata-only revalidation is insufficient because same-size, same-mtime object replacement must remain detectable.

## Deviations
