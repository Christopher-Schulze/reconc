# TASK 383: Remove verified runtime helper duplication

## Why

The runtime retains test-only wrappers, unreachable fallback branches, a hand-rolled exit-error walker, duplicate sorted-key and adapter helpers, dead action-state security shims, and an unused weaker project-key hash. These parallel contracts increase review surface around enforcement code.

## Acceptance

- Every removed symbol or branch has zero production caller and no independent contract.
- Exit-error matching uses `errors.As` with identical wrapped/joined-error behavior.
- Shared collection and adapter helpers retain explicit ordering, deduplication, aliasing, event, fallback-message, and nil/empty semantics.
- Focused tests prove no runtime, task-state, or path identity behavior changes.

## Sub-Tasks

- [x] Re-run whole-repository caller searches for every candidate symbol.
- [x] Remove only proven-dead code and consolidate only byte-identical contracts.
- [x] Update tests that directly exercised obsolete wrappers without weakening coverage.
- [x] Run focused runtime and agent-session tests.

## Notes

- Verified from findings 14, 15, 17, 46, 117, 158, 172, 179, 182, 209, 212, and 214, plus worker finding 471.
- Candidates include `itoa`, `numAsIntDefault`, `matchingPaths`, `parseWorkflowAuditBatchOutput`, duplicate sorted-key helpers, `asExitErr`, `agentsession.projectKey`, test-only JSONL default wrappers, test-only `normalizeMCPRepoPaths` and `recordMCPAudit` wrappers, dead post-`make` nil checks, the tautological fixed-window predicate, production-dead action-state mode/sync shims, the write-only MCP `Observed` field, overlapping Cursor event branches, and duplicated result-reason fallback loops.
- `HasManagedGrokHook` has no production caller, so finding 117 is dead-API cleanup rather than the claimed runtime deduplication failure.
- Similar helpers with different ordering or aliasing contracts must remain separate or receive explicit names; no generic utility package is implied.
- Whole-repository searches reconfirmed that `itoa`, `numAsIntDefault`, `matchingPaths`, `parseWorkflowAuditBatchOutput`, `agentsession.projectKey`, `HasManagedGrokHook`, `normalizeMCPRepoPaths`, and `recordMCPAudit` have only test or benchmark callers. The resolved/lower-level implementations retain all production callers.
- The default-layout JSONL wrappers named by the finding are likewise test-only; exported `Append`, `AppendTransaction`, and `Recover` remain production APIs and are out of removal scope.
- `dedupePreservingOrder`, `dedupeStrings`, and `stableStringCollector` are intentionally not byte-identical: they respectively deduplicate initial values, trim/drop empty values, and preserve duplicate initial values while deduplicating later additions. They remain separate.
- `secureDirectoryMode`, `securePrivateFileMode`, and `syncAndCloseStateDirectory` are exercised only by tests; production action-state security already routes through `privatefs`.
- Removed the verified-dead runtime, JSONL, hook, action-state, and adapter wrappers. Tests now exercise the retained production entry points directly.
- Exit-error extraction now calls `errors.As` directly, preserving standard wrapped and joined error traversal without a parallel walker.
- Consolidated only the byte-identical agent-session clone and result-reason helpers plus the duplicate runtime-package sorted-key helper. Cross-package and semantically distinct collection helpers remain separate.
- Removed the parsed-only `MCPPayload.Observed` field while retaining the externally serialized MCP envelope `observed` field and its adapter-specific values.
- Cursor event classifier tests prove the retained observation and fire-and-forget classifiers are disjoint. Grok drift coverage now exercises `InspectPlatforms` rather than a dead convenience API.
- Action-state tests use canonical `privatefs` enforcement. Production Windows ACL validation remains intact; Windows tests were not executed locally per the requested platform policy.
- Focused gate passed: `go test ./internal/action ./internal/actionstate ./internal/hooks ./internal/jsonl ./internal/runtime ./internal/runtime/agentsession -count=1`.
- Repository fast gate passed: `make test-fast`.
- Full race, release-trust, vet, and lint gates remain reserved for the final queue-wide verification as requested.

## Deviations
