# TASK 454: Reuse Stop fingerprints and loaded evidence

## Why

Stop generation repeatedly hashes the same dirty tree within one attempt and continues threshold walks after the result is already uncacheable. Repository-run mode also loads the complete evidence chain twice before policy evaluation.

## Acceptance

- One Stop attempt reuses safe Git/path/evidence hashes across generation, cache, and stability checks.
- Threshold walks stop once no later observation can restore cache eligibility.
- Repository-run mode passes the already loaded complete evidence into the later policy path.
- Benchmarks prove fewer bytes read/hash operations and lower allocations with identical decisions and race revalidation.

## Sub-Tasks

- [x] Instrument hash/read counts for cold, cache-hit, uncacheable, and run-mode Stop paths.
- [x] Extend the attempt-local scan cache to exact reusable identities.
- [x] Thread loaded evidence without retaining it beyond the attempt.
- [x] Run focused Stop tests and benchmarks.

## Notes

- Verified from findings 197 and 199.
- The attempt-local cache now reuses dirty-file and policy-input content hashes only when the exact path, index entry, resolved target, and platform generation still match. Same-size, same-mtime rewrites force a fresh hash.
- Policy-lock scans provide their already verified lock hash to fingerprint and generation consumers. Cost-threshold walks preflight submodules, then stop exactly when the byte or entry threshold is proven.
- Complete session evidence is threaded from the handler through policy evaluation. Reuse requires the raw evidence revision and every sealed segment generation to remain unchanged; the view is never retained beyond the synchronous Stop attempt.
- Adversarial regressions cover dirty and declared inputs, same-size/mtime rewrites, evidence-segment mutation, policy consumption, cache generations, bounded retries, and repository-run paths. Focused tests passed in 9.42 seconds.
- A two-capture 16 MiB attempt now reports exactly one content read and 16,777,216 hashed bytes instead of two reads. The unchanged evidence benchmark reports exactly one chain load per attempt.
- On Apple M1, the tracked-dirty generation benchmark moved from 888,405 B/op and 4,706 allocs/op to 780,098 B/op and 4,529 allocs/op. The untracked-directory case moved from 5,070,493 B/op and 90,899 allocs/op to 4,965,384 B/op and 90,756 allocs/op; timing remained benchmark-noise sensitive.

## Deviations

- Per user direction, full module, race, vet, lint, release, and platform gates are deferred until TASK 460 so they run once over the final queue state.
