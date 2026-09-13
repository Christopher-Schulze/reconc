# TASK 564: Unify DSH policy and agent workflow

## Why

DSH converted every policy decision into advisory output, unlike the normal Reconc workflow. The user requires equal policy and completion treatment across hosts. Keep host functionality available without blanket compatibility restrictions, and preserve actual host API differences without inventing enforcement guarantees.

## Acceptance

- DSH receives the same policy decisions, explicit CLI/CI completion requirements, and agent workflow as other supported hosts. Remove the DSH-only advisory conversion and exemption in verification.
- Native pre-tool policy blocks are delivered through the documented DSH decision API. Worker/input errors follow the declared pre-tool failure policy. Do not reject native/PTC modes, persistent shells, terminals, delegation, or bridges merely because of their execution mode.
- Stop policy uses the documented bounded continuation API, honors cancellation and reentry, and keeps its actual delivery guarantee explicit in the shared platform model.
- Preserve bounded worker resources, diagnostic deduplication, passive outcome integrity, and user-owned patch handling. Use route budgets for policy decisions rather than an advisory-only deadline.
- Skill and current documentation use common workflow/acceptance rules for every host; keep only necessary event/configuration details per adapter. Historical task records remain historical.
- Complete static DSH configuration reports `configured`; bootstrap and repository sync use the same readiness check for all hosts, without promoting configuration into host loading.
- Offline source-aligned tests prove denied and allowed tools, failures, deadlines, completion, composition, and cleanup. No DSH installation, host/model run, or version publication.
- Propagate generated references, scaffold, pack and watched fixture hashes only after reviewing each changed contract. Complete required checks, archive, commit/push main, and verify GitHub.

## Sub-Tasks

- [x] Verify DSH source contracts and map policy/Stop behavior to the shared host model.
- [x] Update the adapter, normalization and offline verification without compatibility blacklists.
- [x] Update regression contracts, skill, documentation and generated assets.
- [x] Complete local verification and prepare the archived commit for push and exact-commit GitHub verification.

## Notes

The previous conversion in TASK 560 treated the request to keep DSH execution paths available as an unconditional policy exemption. This task supersedes that exemption. Source review uses temporary official upstream files only. Preserve the existing session/transport lifecycle and diagnostic bounds; do not restore the removed compositionConflict blacklist, input freezing, or host-installation requirements. Policy evaluation and honest evidence remain shared; lack of authoritative host outcome fields never creates fabricated success evidence.

Source review confirmed the native pre-tool decision and awaited steering signatures at `deepseek-ai/deepseek-harness` revision `c291e7961a515f6d7af9304e7fd1d257929aef26`, including `packages/core/agent/src/runtime-types.ts`, `packages/core/tools/src/index.ts`, and `packages/llm/llm/src/message.ts`. The existing pinned-release event fixture remains applicable. The final cross-host scan also removed the static-status downgrade and the resulting bootstrap/sync readiness exception.

Targeted adapter and real Go-worker tests cover policy denial/allow, warnings, malformed responses, launch failure, timeout, cancellation, bounded Stop reentry, next-turn reset, disposal, provider composition, and passive result integrity. The obsolete advisory-conversion unit test is replaced by these native-decision contracts and the common all-host policy-denial assertion.

Complete uncached race suites passed for both Go modules, including the shared host matrix and embedded skill bundle. Publication/artifact trust, build, vet and Staticcheck passed. Self-hosting passed after replacing its old 14-configured-entry expectation with all 15 registry entries and including DSH in the same session-start transport loop. Generated documentation and the deterministic scaffold pack match their sources. No DSH installation or agent/model execution was performed.

CI, CodeQL and the separate source-watch workflow are checked against the pushed archived commit; their remote results are reported after push rather than predeclared in this file.

## Deviations

None.
