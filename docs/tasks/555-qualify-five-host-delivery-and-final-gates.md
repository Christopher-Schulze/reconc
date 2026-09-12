# TASK 555: Qualify five-host delivery and final gates

## Why

Adapter unit tests, installed files, binary checks, native host enforcement, hosted CI, and published release assets prove different things. The requested reality check needs one final evidence matrix for the integrated product and explicit remaining limitations.

## Acceptance

- Codex, Devin CLI, Cursor CLI interactive/print, OMP CLI, and DSH each have a version/config-bound qualification record with per-operation results.
- Normal install, skill discovery, missing-skill suggestion, stale-owned-skill update, same-binary repair, and preserved user modifications are exercised through real CLI flows.
- Every supported enforcement claim has a positive control and a linked denied-action proof. Unsupported/blocked/unproven behavior is individually named and cannot be aggregated into full green.
- Required macOS/Linux gates and reference/artifact checks pass for the final source; authenticated live results and hosted CI are reported separately.
- Documentation is consistent with measured capabilities, installation/update behavior, benchmark results, and residual limits.
- All implemented tasks are archived only after their acceptance and validation; commits and any authorized pushes remain one task at a time on main.
- No product tag, release publication, or assigned future version is created by this task.

## Sub-Tasks

- [ ] Assemble the exact-version probe matrix and verify prerequisites without changing active user sessions.
- [ ] Run isolated host and install/update scenarios, recording raw outcomes and boundaries.
- [ ] Resolve in-scope discrepancies in their owning task or queue a concrete new item for separate work.
- [ ] Run final local/hosted verification as authorized and update the canonical documentation.
- [ ] Re-read all changed files and task evidence; archive and report only verified completion.

## Technical Plan

1. Use TASK 545's single verifier/receipt contract. Record host executable/version/digest, Reconc source/build, adapter/config identity, mode, skill identity/discovery, policy/candidate identity, attempted operation, decision, result, side effect, and timestamp. A host with no available authenticated access stays unqualified.
2. For each applicable mode exercise: allowed read/write/shell; denied write/shell; actual nonzero shell; classified MCP allow/deny; first-turn context; compaction; cancellation; session completion; and subagents/background operations where supported. Each absent event is unsupported or unproven with a reason, never silently skipped. Cursor interactive and print have separate rows.
3. Add control runs with hooks disabled and with the intended extension/config shadowed. The verifier must detect loss of protection. Use deliberately harmless disposable markers, not production edits or external side effects. The policy, host config, probes, and repositories remain isolated temporary assets.
4. For live provider-backed tests, inspect available authentication and exact host invocation interfaces first. Apply explicit duration, turn, and cost bounds appropriate to each host; keep noninteractive CI free of provider credentials by default. Missing DSH installation/authentication is a visible prerequisite to fulfill during authorized implementation, not permission to install it during this planning turn.
5. Exercise official install flows in temporary homes, then prove discovery/loading through each host's supported mechanism. Run update check/apply across the TASK 552 state table. Verify archive/binary embedded payload equivalence and release-trust fixtures without publishing a release.
6. Final local gates: `make test-fast`, `make test`, `make vet`, `make lint`, `make self-host`, `make coverage` for both modules as review evidence, and `git diff --check`. `make test` already includes publication audit and release trust; rerun a phase only for changed inputs or an unresolved failure. TASK 554 owns the complete benchmark evidence. Maintain free-disk/cache checks before heavy work.
7. Preserve Windows implementation and release artifact definitions. Do not run automatic Windows suites; only an explicitly requested smoke check with a hard two-minute job limit is allowed. Complete Linux/macOS validation remains the product gate.
8. Flush product behavior into `docs/documentation.md`; update README, generated CLI/reference docs, embedded guide, and portable skill references only where their promises changed. Keep task history/evidence in task details. Preserve documentation for other hosts and the existing optional MCP gateway.
9. During implementation, finish and verify one task before its commit, inspect its diff, archive its detail with a true move, and update docs/tasks.md. Push only within the user's actual execution authorization, to origin/main. After a push, bind GitHub CI/CodeQL/artifact checks to the exact remote commit and report pending/failure honestly. Never infer hosted green from local checks or push success.

## Verification Matrix

| Dimension | Required proof |
| --- | --- |
| Adapter contract | Generated configuration/extension executes against the pinned host API |
| Native enforcement | Host attempted the identified operation; matching deny and independent unchanged target |
| Positive control | Same host/config performed the corresponding allowed operation |
| Tool evidence | Native typed outcome or trusted command receipt, not hook exit or prose |
| Skill delivery | Verified bundle, owned install receipt, resolved host skill identity |
| Update | Read-only check; correct missing/stale/modified state; transactional apply/recovery |
| Performance | TASK 554 paired source-bound artifacts and complete target inventory |
| Local/hosted checks | Exact commands and commits, individual outcome, no stale aggregate green |

## Dependencies

TASKS 544-554. This is the final integration gate, not a second owner for each host implementation.

## Shared Execution Contract

All tasks in this batch are queued plans created on 2026-09-12. Before starting implementation, re-read the task board, detail, actual source signatures, current host versions, and applicable AGENTS.md. Revalidate drifting upstream docs before coding. Apply minimal patches, preserve unrelated/dirty files, use isolated repositories, run focused tests per logical sub-task and the required completion gates, and keep claims tied to retained evidence. A task whose required host proof is blocked remains open or blocked; it is not archived as fully qualified.

## Notes

Starting source: `25ce22668769dbbb8c2360f8db6e7ecc574033e1`. Planning inspected registry/generators, native runtime adapters, live verification, install/update ownership, skill content/release assets, release-trust classification, retained benchmarks, current official docs, and pinned DSH/OMP source. Source URLs, file sizes, and SHA-256 digests for retained upstream files are recorded in `.build/planning-agent-integrations/source-manifest-complete.json`; task-specific public source links remain usable without that ignored local evidence. It did not execute authenticated live host enforcement, a new full benchmark, or current hosted CI for modified product code. No product implementation was changed in the planning pass.

The native-hook boundary cannot prevent an operator from disabling/unloading the host extension or bypassing the host entirely. Documentation must identify those limits and the deterministic CLI/Git/CI backstop without presenting a skill or MCP gateway as universal enforcement.

## Deviations

None.
