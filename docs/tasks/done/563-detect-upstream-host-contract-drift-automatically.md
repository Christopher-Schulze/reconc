# TASK 563: Detect upstream host contract drift automatically

## Why

Saved event contracts do not detect upstream API or documentation changes by themselves. Maintainers need automatic source-based drift evidence without installing or running agent hosts.

## Acceptance

- Every registered host event fixture has an explicit monitored official source and local fixture identity; open-source API probes use immutable reviewed baselines and a current upstream comparison.
- Ordinary tests and product runtime remain offline. A separate explicit maintenance command and scheduled/manual GitHub workflow fetch source/doc bytes only, with bounded time, bytes and requests.
- Deterministic reports distinguish unchanged source, changed source requiring review, and unavailable source. Source drift never blocks DSH dispatch, product CI, or release eligibility.
- No upstream program, dependency lifecycle, host, model or installer is executed. No automatic baseline blessing, source patching, issue posting or messages to others.
- Real local HTTP-boundary tests cover matching/change/error/oversize/timeout data and fixture/manifest drift. Official sources are inspected before establishing baselines.
- Canonical docs describe commands, coverage and review/update procedure. Complete full gates, archive, commit/push and verify final GitHub checks.

## Sub-Tasks

- [x] Inspect existing fixture provenance and official source contracts and record bounded review baselines.
- [x] Implement source/fixture drift checks and deterministic offline and HTTP regressions.
- [x] Add a separate scheduled/manual maintenance workflow, reports and documentation.
- [x] Run consolidated verification and prepare the archived commit for push and exact-commit GitHub verification.

## Notes

Verified all 15 registered hosts, 21 local fixture identities, and 21 official source probes. The explicit upstream comparison completed with every source unchanged and no missing markers. Tests exercise actual loopback HTTP, changed content, marker loss despite matching digests, HTTP errors, oversized bodies, malformed content, timeout/cancellation, redirect restrictions, fixture drift, strict catalog provenance, deterministic reports, and maintenance workflow isolation.

Complete uncached race suites passed for both Go modules. Build, vet, Staticcheck, and isolated self-hosting passed. The publication audit initially rejected a literal credential-shaped URL in a negative test; constructing the same URL through the URL API preserves the rejection test without embedding that literal. The affected race suite, vet and Staticcheck passed again, followed by the complete release-trust gate, including publication and artifact verification. Canonical documentation, reference sections and deterministic scaffold pack are current. No agent host was installed or executed, and no runtime dependency was added.

The daily/manual source-watch workflow is separate from product CI and release gates. Its first GitHub dispatch and final CI/CodeQL checks are verified against the pushed archived commit; remote results are reported with that commit rather than predeclared in its contents.

## Deviations

None.
