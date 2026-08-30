# TASK 449: Retain and stream session evidence safely

## Why

Failed or tainted sessions can retain up to 64 MiB of rotated evidence outside retention. Cold loads merge the full chain in memory, the verified-prefix cache still reads and hashes every segment, and transient filesystem errors are persisted as permanent integrity taint.

## Acceptance

- Evidence segments have chain-safe age/count/byte retention that protects active and unresolved evidence.
- Cold evaluation enforces an aggregate decoded/merged budget and avoids retaining the complete chain when streaming is sufficient.
- Verified unchanged prefixes skip safe redundant reads through identity-bound generations without trusting path metadata alone.
- Retryable I/O failures remain retryable; digest/shape/identity violations alone create durable integrity taint.
- Tests and benchmarks cover 64-segment chains, tainted/abandoned sessions, transient errors, replacement, cache hits, and retention pressure.

## Sub-Tasks

- [x] Define evidence lifecycle, total-memory, and error-taxonomy contracts.
- [x] Integrate segment classes into retention with active/taint protection.
- [x] Stream merging and add an identity-safe unchanged-prefix fast path.
- [x] Run focused evidence, retention, Stop tests, and benchmarks.

## Notes

- Verified from findings 188, 191, 200, and 202.
- Evidence chains now stream one segment at a time under a conservative 16 MiB merged-memory budget. Cache entries retain one bounded merged snapshot plus compact identity generations instead of every decoded segment.
- Stable unchanged prefixes skip segment reads and hashes; appended chains decode only their suffix. Replacement or generation drift forces a safe full-chain decode. Platforms without a reliable generation fall back to uncached bounded decoding.
- Only JSON/shape, declared identity, digest, linkage, chain-head, and impossible segment-count failures create chain-integrity taint. Transient reads and filesystem replacement races remain retryable; aggregate exhaustion records a distinct capacity taint.
- Retention applies 14-day, 32-session-directory, 64 MiB class bounds plus the existing 16 MiB total-state bound. It protects active and unresolved-taint chains, validates up to 64 contiguous regular members, acquires the session lock, revalidates, and deletes only the complete directory.
- Focused evidence, retention, Stop, cleanup, transient-error, replacement, cache-hit, 64-segment, pressure, and lease tests passed.
- Focused 100 ms benchmark on darwin/arm64: cold 64-segment load 1.977 ms, 538,269 B, 7,444 allocs; verified-prefix load 0.640 ms, 238,326 B, 5,159 allocs.

## Deviations

- Per Christopher's queue-wide gate instruction, broad full/race/vet/lint/release gates are deferred until TASK 460. One broad retention package run was stopped after it exceeded 30 seconds; no result from that aborted run is claimed.
