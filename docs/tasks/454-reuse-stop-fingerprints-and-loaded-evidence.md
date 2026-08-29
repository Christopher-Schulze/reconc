# TASK 454: Reuse Stop fingerprints and loaded evidence

## Why

Stop generation repeatedly hashes the same dirty tree within one attempt and continues threshold walks after the result is already uncacheable. Repository-run mode also loads the complete evidence chain twice before policy evaluation.

## Acceptance

- One Stop attempt reuses safe Git/path/evidence hashes across generation, cache, and stability checks.
- Threshold walks stop once no later observation can restore cache eligibility.
- Repository-run mode passes the already loaded complete evidence into the later policy path.
- Benchmarks prove fewer bytes read/hash operations and lower allocations with identical decisions and race revalidation.

## Sub-Tasks

- [ ] Instrument hash/read counts for cold, cache-hit, uncacheable, and run-mode Stop paths.
- [ ] Extend the attempt-local scan cache to exact reusable identities.
- [ ] Thread loaded evidence without retaining it beyond the attempt.
- [ ] Run focused Stop tests and benchmarks.

## Notes

- Verified from findings 197 and 199.

## Deviations
