# TASK 237: Remove residual implementation scaffolding

## Why

The TASK 223-236 reality check found two internal artifacts that no longer
serve production behavior and one stale evaluator comment. Discovery retains a
private compiled-lockfile state that production never reads, the parser keeps a
mapping-only YAML wrapper used exclusively by tests, and `evalContext` is still
documented as a removed constant. Removing only these verified remnants makes
the implementation match its actual contracts without changing behavior.

## Acceptance

- Discovery retains the canonical lockfile path, exact missing-warning
  identity, immutable post-publication copy, and deterministic output without a
  private state field or state-only assertions.
- Parser bounds, single-decode production behavior, legacy two-pass
  differential coverage, and fuzz coverage remain intact without a
  test-only mapping wrapper in production code.
- `evalContext` documentation describes the current evaluation-owned state and
  contains no reference to the removed `repoRootKey` constant.
- Focused ingest, parser, compiler, and runtime tests pass together with
  formatting, vet, Staticcheck, and module-tidiness checks.

## Sub-Tasks

- [x] Remove the unread discovery state and state-only assertions
- [x] Remove the test-only YAML mapping wrapper and retarget its tests
- [x] Correct the stale evaluator context comment
- [x] Run focused verification and repository-level consistency checks
- [x] Re-read the final diff, archive the TASK, and commit atomically

## Notes

- Scope is restricted to the three findings approved after the TASK 223-236
  audit. No public contract, schema, generated artifact, or release surface
  changes.
- `go test -race ./internal/ingest ./internal/parser ./internal/compiler
  ./internal/runtime`, focused `go vet`, Staticcheck v0.8.0, `go mod
  tidy -diff`, formatting, stale-symbol search, and `git diff --check` all
  pass.
- The final source review confirms that discovery still publishes an immutable
  canonical lockfile path, parser tests still exercise the bounded document
  decoder and legacy two-pass differential path, and evaluator behavior is
  unchanged.

## Deviations

None.
