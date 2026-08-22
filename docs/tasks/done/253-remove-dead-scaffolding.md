# TASK 253: Remove verified low-risk scaffolding and dead branches

## Why

The audit confirmed a bounded set of production-dead helpers, misleading
comments, hand-written formatters, unreachable branches, an empty warning
channel, and an unused workflow authorization. They do not justify separate
features, but retaining them increases code volume, false maintenance surface,
and future agent ambiguity.

## Acceptance

- Policy-fragment loading no longer returns an always-empty warning slice; the
  caller contract is simplified without suppressing any real warning.
- Runtime and CLI integer formatting use `strconv` with identical output for
  zero, positive, negative, and minimum integer values.
- `compileRepoPolicyWithLoader`, text-based include/preset helpers,
  `enableCodexHooks`, unreachable user-template empty guards, and the yaml.v2
  map-key branch are removed only after their tests and benchmarks are migrated
  to production entry points or direct document helpers.
- Release-trust authorizes only actions used by current workflows.
- The context-size comment describes current ordering and no nonexistent flag.
- Command suggestion and impact-manifest control flow contains no provably
  unreachable distance or length checks; observable behavior remains unchanged.
- No exported API, schema, artifact identity, release behavior, or supported
  CLI output changes as a side effect of cleanup.
- Search proves each removed symbol or stale phrase absent. Focused tests,
  benchmarks, formatting, Vet, Staticcheck, race tests, release trust, harness
  pack determinism, and full gates pass.

## Sub-Tasks

- [x] Remove the always-empty fragment warning channel
- [x] Replace remaining hand-written integer formatters with `strconv`
- [x] Migrate tests and benchmarks off production-dead compiler and ingest helpers
- [x] Remove dead bootstrap/template compatibility scaffolding
- [x] Reconcile workflow action authorization with actual workflow use
- [x] Correct context-size wording and simplify unreachable control flow
- [x] Search for residual symbols and run focused plus full verification
- [x] Update documentation only where removed scaffolding was described

## Notes

- External findings: F-12, F-25, F-26, F-30, F-40, F-85, F-86, F-90,
  F-97, F-105, and F-106.
- This task is intentionally last. Earlier functional tasks may naturally remove
  some items; re-audit each symbol before editing so cleanup never duplicates or
  conflicts with a real implementation change.
- No formatter, codemod, or broad rewrite is authorized. Every removal is a
  symbol-specific surgical diff with caller and test searches.
- `actions/upload-artifact` was already absent from the current release-trust
  allowlist and every current authorization is used by a workflow, so no script
  edit was required for F-40.
- The removed items were private implementation scaffolding only. No product
  documentation described them, so the correct documentation propagation is
  the TASK record itself rather than a user-facing contract change.
- Focused tests passed for ingest, runtime, CLI, compiler, bootstrap, templates,
  context size, command metadata, and Impact Lab. The retained single-parse
  repository-config benchmark also ran successfully.
- Residual-symbol and stale-phrase searches are empty for every removed item.
  Current workflows use exactly the seven actions authorized by release trust.
- `make test` passed, including formatting, publication audit, deterministic
  harness-pack verification, full root and template race suites, and the local
  release-trust fixture. `make vet`, `make lint`, and typed TASK validation also
  passed.

## Deviations

None.
