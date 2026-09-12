# TASK 543: Clarify agent integration and verify portable guidance

## Why

Agent onboarding must distinguish guidance, CLI evaluation, live hooks, remote CI, and opt-in MCP routing. The skill currently suggests a product-root `.reconc/cache` build, does not explicitly branch for read-only work, and demonstrates a proof destination that can change its own candidate.

## Acceptance

- The portable skill and embedded guide preserve read-only scope and existing authorization, explain optional skill discovery and MCP routing, and keep compact entrypoints within their existing budgets.
- Source-build guidance follows the product Makefile without bootstrapping or installing policy in this product repository.
- Actual CLI tests demonstrate non-mutating agent entry and the candidate-binding consequences of inside/outside proof destinations.
- Current docs and required validation pass; archive, commit, and push origin/main without publishing a release.

## Sub-Tasks

- [x] Inspect current GitHub checks, skill, embedded guide, CLI behavior, existing tests, and retained performance evidence.
- [x] Correct guidance and document the integration choices; add real isolated CLI regressions.
- [x] Run focused checks and repository gates, re-read the changes, archive, commit, push, and verify exact remote CI.

## Notes

- Christopher authorized the review, concrete improvements, tests, documentation, commit, and push. Starting source: 26a1485ac55fe98b2e12f0dcfbfa41e7cc26c733.
- CI and CodeQL for that source are green. The portable skill exists in the repository but is absent from this host's standard user skill directories. No host configuration mutation is required for this task.
- Preserve current runtime guarantees and all private historical task files. Use isolated temporary repositories for behavioral tests.
- CLI controls reproduce a valid outside-repository proof and an integrity-valid but candidate-mismatched inside-repository proof. Read-only entry snapshots compare file membership, content, mode, size, and modification time for both missing-lockfile and changed-source fixtures.
- The fixture owns AGENTS.md, so discovery correctly reports a missing compiled lockfile rather than absent configuration; changed sources report a source_digest mismatch. Test assertions use these verified runtime diagnostics, without changing runtime behavior.
- README propagation also corrects verified documentation drift: the CLI dependency graph includes jsonschema/v6 and regexp2; Git tracks the task board and selected details despite default ignores; the live main ruleset has an always-mode repository-role bypass. Documentation now distinguishes push acceptance from exact-commit CI and published release examples from later development source. No repository settings or release state are changed.
- The first full gate passed all uncached root and template race tests, then macOS awk reported a regular-expression syntax error while scanning a Graphify cache file. Inspection found valid ASCII JSON, not binary content. The same file passed 21 isolated checks; the complete classifier controls and repository scan also passed unchanged, including the release-trust rerun. No root cause or classifier repair is claimed, and no scan exclusions or weakened assertions were introduced. Logs are retained under .build/review-repairs/task543-*.
- Local validation passed: focused real CLI controls, agent-guide and publication contracts, portable skill validation, publication audit, uncached root/template race suites, the separate release-trust rerun (real release fixture: 77 seconds), make vet, make lint, make self-host, and git diff --check. The first make test invocation itself failed as recorded above; its failed release-trust phase passed when rerun unchanged. All 25 original private historical files remain unchanged and untracked. Hosted checks for the resulting commit are verified after pushing and reported separately.

## Deviations

None.
