# TASK 015: Universal source hygiene and final polish

## Why

The final Golem comparison still exposes two reusable source-quality checks and several standalone-product drift points. They need an agnostic, bounded implementation without importing Golem-specific policy, subprocess overhead, stale CLI compatibility, or misleading historical planning state.

## Acceptance

- Changed Go files can be checked for canonical formatting through native assurance without Git or formatter subprocesses.
- Changed shipped source can be checked for high-signal implementation-debt markers through native assurance with explicit path exclusions and exemptions.
- The default agent and Go assurance packs expose the new checks in non-blocking adoption mode.
- Bootstrap, hook-platform naming, generated configuration guidance, and current documentation consistently describe the supported eight agent CLIs and current runtime behavior.
- Removed Gemini compatibility and frozen v0.4 planning material cannot be mistaken for current product configuration or current task truth.
- No project-specific Golem policy is copied into the standalone product.
- Tests, race tests, vet, static analysis, build, self-hosting, and release-trust checks pass.

## Sub-Tasks

- [x] Implement bounded native Go-format and source-hygiene assurance.
- [x] Integrate the assurance gates into policy packs and bootstrap coverage.
- [x] Correct bootstrap, platform naming, configuration, and documentation drift.
- [x] Make the legacy v0.4 planning corpus unambiguously historical.
- [x] Run a fresh-eyes audit, the full verification matrix, and close the TASK.
- [x] Eliminate the self-host pre-commit's process/concurrency false friction and reverify the amended commit.

## Notes

- Golem remains read-only. Its reusable mechanisms are inputs for independent agnostic design, not source files to copy.
- Project-specific dependency graphs, proof ledgers, Mothership gates, UI gates, and module contracts remain outside standalone reconc.
- The reusable Golem source checks became native changed-file `go_format` and `source_hygiene` assurance; no Golem path, command, or project semantic was imported.
- The template no longer treats `.gemini/` as a supported local agent state directory; Antigravity remains owned by `.agents/`.
- The locally present v0.3/v0.4 `docs/todo*` corpus is Git-ignored and untracked. Current docs now state explicitly that it is frozen scratch and that `docs/tasks.md` is the only current TASK overview.
- Fresh-eyes comparison found no further agnostic Golem mechanism worth importing. The only Golem-only control-loop implementation is the intentionally removed Degenmode; remaining extra gates bind Golem-specific architecture, research, proof-ledger, Mothership, UI, or module semantics.
- Source-hygiene quote/comment awareness prevents sentinel examples from creating false positives while retaining Rust dereference and decorated-doc-comment coverage.
- The complete 11-kind assurance positive matrix passes. The combined native changed-source benchmark over 1,000 Go files measured 45.82 ms on Apple M1, about 46 microseconds per file, with no Git or formatter subprocess.
- Final verification passed for root and template-harness tests, race tests, vet, Staticcheck v0.7.0, binary build, self-hosting, release trust, strict policy refresh, TASK validation, and a terse policy check.
- Deep doctor reports only one local environment drift: this checkout's already installed Codex hook artifact predates the current generated contract. It was not reinstalled or activated because hook installation was explicitly outside scope; generator, registry, scaffold, and self-host proofs are green.
- The self-host pre-commit now passes with zero violations: the Go concurrency gate recognizes only complete local WaitGroup ownership, while every direct workflow-audit subprocess has a context deadline and bounded post-cancel process/pipe wait.

## Deviations

None.
