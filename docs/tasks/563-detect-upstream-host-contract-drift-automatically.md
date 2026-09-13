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

- [ ] Inspect existing fixture provenance and official source contracts and record bounded review baselines.
- [ ] Implement source/fixture drift checks and deterministic offline and HTTP regressions.
- [ ] Add a separate scheduled/manual maintenance workflow, reports and documentation.
- [ ] Run consolidated verification, archive, commit, push and verify GitHub.

## Notes

Reuse existing host-events fixtures and platform registry as coverage truth. Implement maintenance tooling in Go using existing bounded IO/HTTP helpers where applicable; do not add a runtime network path or another dependency. Monitor relevant API source files and official documentation rather than upstream executables. A content change is a review signal, not proof of a breaking change. Keep periodic maintenance separate from required CI and upload redacted digest/status artifacts instead of source bodies. Network errors remain distinguishable from unchanged content. Explicit user approval covers this requested automatic source comparison workflow, not external messages or release publication.

## Deviations

None.
