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

- [ ] Remove the always-empty fragment warning channel
- [ ] Replace remaining hand-written integer formatters with `strconv`
- [ ] Migrate tests and benchmarks off production-dead compiler and ingest helpers
- [ ] Remove dead bootstrap/template compatibility scaffolding
- [ ] Reconcile workflow action authorization with actual workflow use
- [ ] Correct context-size wording and simplify unreachable control flow
- [ ] Search for residual symbols and run focused plus full verification
- [ ] Update documentation only where removed scaffolding was described

## Notes

- External findings: F-12, F-25, F-26, F-30, F-40, F-85, F-86, F-90,
  F-97, F-105, and F-106.
- This task is intentionally last. Earlier functional tasks may naturally remove
  some items; re-audit each symbol before editing so cleanup never duplicates or
  conflicts with a real implementation change.
- No formatter, codemod, or broad rewrite is authorized. Every removal is a
  symbol-specific surgical diff with caller and test searches.

## Deviations

None.
