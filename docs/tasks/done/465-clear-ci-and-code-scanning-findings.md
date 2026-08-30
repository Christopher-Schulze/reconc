# TASK 465: Clear CI and code-scanning findings

## Why

The TASK 464 push exposed a synthetic Windows host-path fixture to the publication audit, a timing-dependent macOS concurrency assertion, and five open High/Critical CodeQL findings involving allocation-size arithmetic and manually quoted diagnostics. The public main branch must pass every CI gate and retain no open CodeQL, Dependabot, or secret-scanning alert.

## Acceptance

- The publication audit accepts the current tree and records only an exact, owned exception for the immutable synthetic historical blob.
- The adopt concurrency regression deterministically proves that `Apply` shares the non-blocking repository transaction lock.
- Allocation capacity arithmetic cannot overflow in the three CodeQL-reported paths.
- Composite diagnostics quote untrusted rule identities through the established escaping helper, with adversarial regression coverage.
- Focused local tests and short repository gates pass without disabling tests or changing the product version.
- Automatic GitHub CI is green on Linux, macOS, and Windows; CodeQL completes with zero open alerts; Dependabot and secret scanning retain zero open alerts.

## Sub-Tasks

- [x] Reproduce and verify every CI and code-scanning finding against current source.
- [x] Fix the publication boundary without weakening current-tree scanning.
- [x] Make the adopt transaction-lock regression deterministic.
- [x] Guard allocation sizing and diagnostic quoting with adversarial tests.
- [x] Run focused verification and short repository gates.
- [x] Archive, commit, push, and verify GitHub CI plus security alerts.

## Notes

- GitHub Actions run `33328995715` failed Ubuntu and Release Trust on `internal/impactlab/privacy_test.go:24` plus immutable blob `9eab0a0433829ad8b347aa40426ddaba6aa1c233:24`; macOS failed `TestApplySerializesConcurrentRepositoryMutations` because both fast transactions completed without guaranteed overlap.
- CodeQL alerts 61-65 identify unchecked allocation arithmetic in the runtime stable collector, canonical lockfile member insertion, and JSONL record framing, plus manual single-quote diagnostic construction in composite evaluation.
- Dependabot and secret scanning currently report zero open alerts.
- Focused regressions passed across publication, Impact Lab, adopt, compiler, JSONL, and runtime packages. Complete affected-package tests, `make fmt-check`, `make vet`, and `make lint` passed.
- `make publication-audit` passed over 1,771 tracked files, 473 post-boundary commits, and 6,197 post-boundary blobs; the harness-pack integrity check also passed.

## Deviations

- Broad local `make test-fast`, race, release-trust, and Windows suites were not duplicated because the user explicitly required minimal local runtime. The pushed commit's automatic GitHub workflows are the final full-platform and CodeQL evidence.
