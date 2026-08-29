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

- [ ] Define evidence lifecycle, total-memory, and error-taxonomy contracts.
- [ ] Integrate segment classes into retention with active/taint protection.
- [ ] Stream merging and add an identity-safe unchanged-prefix fast path.
- [ ] Run focused evidence, retention, Stop tests, and benchmarks.

## Notes

- Verified from findings 188, 191, 200, and 202.

## Deviations
