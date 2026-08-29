# TASK 383: Remove verified runtime helper duplication

## Why

The runtime retains test-only wrappers, unreachable fallback branches, a hand-rolled exit-error walker, duplicate sorted-key and adapter helpers, dead action-state security shims, and an unused weaker project-key hash. These parallel contracts increase review surface around enforcement code.

## Acceptance

- Every removed symbol or branch has zero production caller and no independent contract.
- Exit-error matching uses `errors.As` with identical wrapped/joined-error behavior.
- Shared collection and adapter helpers retain explicit ordering, deduplication, aliasing, event, fallback-message, and nil/empty semantics.
- Focused tests prove no runtime, task-state, or path identity behavior changes.

## Sub-Tasks

- [ ] Re-run whole-repository caller searches for every candidate symbol.
- [ ] Remove only proven-dead code and consolidate only byte-identical contracts.
- [ ] Update tests that directly exercised obsolete wrappers without weakening coverage.
- [ ] Run focused runtime and agent-session tests.

## Notes

- Verified from findings 14, 15, 17, 46, 117, 158, 172, 179, 182, 209, 212, and 214, plus worker finding 471.
- Candidates include `itoa`, `numAsIntDefault`, `matchingPaths`, `parseWorkflowAuditBatchOutput`, duplicate sorted-key helpers, `asExitErr`, `agentsession.projectKey`, test-only JSONL default wrappers, test-only `normalizeMCPRepoPaths` and `recordMCPAudit` wrappers, dead post-`make` nil checks, the tautological fixed-window predicate, production-dead action-state mode/sync shims, the write-only MCP `Observed` field, overlapping Cursor event branches, and duplicated result-reason fallback loops.
- `HasManagedGrokHook` has no production caller, so finding 117 is dead-API cleanup rather than the claimed runtime deduplication failure.
- Similar helpers with different ordering or aliasing contracts must remain separate or receive explicit names; no generic utility package is implied.

## Deviations
